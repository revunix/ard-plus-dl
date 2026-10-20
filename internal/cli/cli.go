// Package cli is the headless entry point: argument handling, dependency
// checks, wiring and exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/auth"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/dash"
	"github.com/revunix/ard-plus-dl/internal/download"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/secret"
	"github.com/revunix/ard-plus-dl/internal/store"
	"github.com/revunix/ard-plus-dl/internal/tui"
)

// Version is the build version, injected through -ldflags.
var Version = "dev"

// Usage is printed for --help.
const Usage = `Usage: ard-plus-dl [options] <url> [username password] [skip]
       ard-plus-dl [options] --links-file <file> [username password] [skip]
       ard-plus-dl --library | --check | --download-new | --add <url>

Options (they must come before the URL):
  --automatic            run without interactive prompts
  --links-file <file>    read URLs from <file> (batch mode)
  --force-redownload     delete existing files and download again
  --library              list the stored series and their new episodes
  --check                check every stored series for new episodes
  --download-new         check and download the new episodes
  --add <url>            add a URL to the library without downloading
  -h, --help             show this help and exit
  -v, --version          show the version and exit

Environment: ARD_PLUS_USER, ARD_PLUS_PASSWORD, ARD_PLUS_TAG (Datei-Tag is mandatory),
             ARD_PLUS_CREDENTIALS_FILE, ARD_PLUS_FFMPEG, ARD_PLUS_FFPROBE,
             ARD_PLUS_DEBUG_DIR (schreibt manifest.mpd, tracks.txt, requests.log),
             ARD_PLUS_LIBRARY (SQLite-Datei), DOWNLOADS_DIR, XDG_STATE_HOME, XDG_CONFIG_HOME

Requirements: ffmpeg and ffprobe on PATH. Without a terminal the tool runs
headless; with a terminal it opens the interactive UI.`

// Run executes the command line and returns the process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := config.ParseArgs(args)
	missingURL := errors.Is(err, config.ErrNoURL)
	switch {
	case errors.Is(err, config.ErrHelp):
		fmt.Fprintln(stdout, Usage)
		return 0
	case errors.Is(err, config.ErrVersion):
		fmt.Fprintf(stdout, "ard-plus-dl %s\n", Version)
		return 0
	case err != nil && !missingURL:
		fmt.Fprintln(stderr, err)
		return 1
	case missingURL:
		opts = &config.Options{Skip: 1}
	}
	explicitURL := opts.URL != ""

	env := config.OSEnv
	stdinFile, stdinIsFile := stdin.(*os.File)
	stdinTTY := stdinIsFile && term.IsTerminal(int(stdinFile.Fd()))
	stdoutFile, stdoutIsFile := stdout.(*os.File)
	stdoutTTY := stdoutIsFile && term.IsTerminal(int(stdoutFile.Fd()))
	interactive := useTUI(opts, stdinTTY, stdoutTTY)

	config.ResolveCredentials(opts, env, stderr)

	opts.WorkDir, err = resolveWorkDir(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	opts.LibraryPath = config.LibraryPath(env, opts.WorkDir)

	state, storeErr := store.Open(opts.LibraryPath)
	if storeErr != nil {
		fmt.Fprintf(stderr, "Warnung: Bibliothek nicht verfügbar (%v)\n", storeErr)
		state = nil
	} else {
		defer state.Close()
	}
	secrets := secret.Open(opts.CredentialsFile)
	prefill(opts, state, secrets, interactive)

	if opts.LibraryMode != "" {
		return runLibraryMode(ctx, opts, env, stdout, stderr, state, interactive, stdin, stdinTTY)
	}

	if opts.Batch() {
		if !fileExists(opts.LinksFile) {
			fmt.Fprintf(stderr, "Links file missing or not found: %s\n", opts.LinksFile)
			return 1
		}
	} else if opts.URL == "" {
		if !interactive {
			fmt.Fprintln(stderr, config.ErrNoURL)
			return 1
		}
	} else {
		opts.URL = media.NormalizeURL(opts.URL)
		if err := config.ValidateContentURL(opts.URL); err != nil {
			fmt.Fprintf(stderr, "Invalid URL: %s\n", err)
			return 1
		}
	}
	if err := opts.ResolveSkip(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	ffmpeg, ffprobe, err := checkDependencies(env)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	opts.DownloadsDir = env("DOWNLOADS_DIR")
	if opts.DownloadsDir == "" {
		opts.DownloadsDir = filepath.Join(opts.WorkDir, "downloads")
	}
	if !ensureWritableDir(opts.DownloadsDir) {
		fmt.Fprintf(stderr, "Downloads directory is not writable: %s\n", opts.DownloadsDir)
		return 1
	}
	opts.TokenFile = config.TokenPath(env, opts.WorkDir)
	hasToken := fileExists(opts.TokenFile)

	// The interactive UI collects the Datei-Tag and missing credentials on its
	// start screen; only the headless paths prompt on the terminal.
	if !interactive {
		if opts.FileTag == "" && stdinTTY {
			fmt.Fprint(stdout, "Datei-Tag (z. B. example): ")
			opts.FileTag = readLine(ctx, stdin)
		}
		if err := config.ValidateFileTag(opts.FileTag); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !hasToken && (opts.Username == "" || opts.Password == "") && stdinTTY {
			promptCredentials(ctx, opts, stdin, stdout, stdinFile)
		}
		if !hasToken && (opts.Username == "" || opts.Password == "") {
			fmt.Fprint(stderr, credentialsHelp(opts))
			return 1
		}
	}

	logs := newLogRouter(stdout)
	session := buildSession(opts, logs)
	debugDir := resolveDebugDir(env)
	if debugDir != "" {
		logs.Logf("Diagnose-Dateien landen in: %s", debugDir)
	}
	application := buildApp(opts, session, ffmpeg, ffprobe, stdout, logs, state, debugDir)
	if client, ok := session.Playback.(*ardplus.Client); ok && debugDir != "" {
		client.Debugf = logs.Logf
	}
	persistCredentials(opts, state, secrets)

	if interactive {
		needsStart := opts.URL == "" || opts.FileTag == "" ||
			(!hasToken && (opts.Username == "" || opts.Password == ""))
		ensureSession := func(callCtx context.Context) error {
			return session.Ensure(callCtx)
		}
		ui := tui.NewUI(tui.Options{
			Context:     ctx,
			Config:      opts,
			StartScreen: needsStart,
			AutoStart:   explicitURL && !needsStart,
			NeedsLogin:  !hasToken,
			Store:       state,
			Secrets:     secrets,
			Run: func(runCtx context.Context) error {
				return application.Run(runCtx)
			},
			Check: func(checkCtx context.Context) ([]app.CheckResult, error) {
				if err := ensureSession(checkCtx); err != nil {
					return nil, err
				}
				return application.CheckLibrary(checkCtx)
			},
			DownloadNew: func(newCtx context.Context, entry store.Series) error {
				if err := ensureSession(newCtx); err != nil {
					return err
				}
				return application.DownloadNew(newCtx, entry)
			},
			DownloadEpisodes: func(newCtx context.Context, entry store.Series, ids []string) error {
				if err := ensureSession(newCtx); err != nil {
					return err
				}
				return application.DownloadEpisodes(newCtx, entry, ids)
			},
			RefreshEntry: func(refreshCtx context.Context, entry store.Series) error {
				if err := ensureSession(refreshCtx); err != nil {
					return err
				}
				return application.CheckEntry(refreshCtx, entry).Err
			},
		})
		logs.SetSink(func(line string) {
			ui.Event(app.Event{Kind: app.EventLog, Message: line})
		})
		// App.logf already forwards to UI.Event; avoid a second log event.
		application.Logf = nil
		application.UI = ui
		final, runErr := ui.Program().Run()
		// The program holds the model as a pointer, so the final model is a
		// *tui.Model as well.
		model, isModel := final.(*tui.Model)
		if isModel {
			model.Close()
		}
		if runErr != nil {
			fmt.Fprintln(stderr, runErr)
			return 1
		}
		if isModel {
			return model.ExitCode()
		}
		return 0
	}

	application.UI = &headlessUI{
		out:         stdout,
		in:          stdin,
		interactive: stdinTTY,
	}
	if err := application.Run(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// prefill fills missing values from the library and the password store.
func prefill(opts *config.Options, state *store.Store, secrets secret.Store, interactive bool) {
	if opts.Username == "" && state != nil {
		opts.Username = setting(state, store.SettingUsername)
	}
	if opts.FileTag == "" && state != nil {
		opts.FileTag = setting(state, store.SettingFileTag)
	}
	if opts.Password == "" && secrets != nil {
		if password, err := secrets.Get(); err == nil {
			opts.Password = password
		}
	}
	// The last URL only pre-fills the interactive start screen; a headless
	// invocation without arguments must still fail.
	if interactive && opts.URL == "" && state != nil {
		opts.URL = setting(state, store.SettingLastURL)
	}
}

func setting(state *store.Store, key string) string {
	value, err := state.Setting(key)
	if err != nil {
		return ""
	}
	return value
}

// persistCredentials stores what the run resolved, so the next start has it.
func persistCredentials(opts *config.Options, state *store.Store, secrets secret.Store) {
	if state != nil {
		_ = state.SetSetting(store.SettingUsername, opts.Username)
		_ = state.SetSetting(store.SettingFileTag, opts.FileTag)
	}
	_ = secrets
}

// logRouter lets components log before the UI exists and reroutes their
// output into the running TUI afterwards. Without this, direct stdout writes
// would corrupt the alternate screen.
type logRouter struct {
	mu   sync.Mutex
	sink func(line string)
}

func newLogRouter(out io.Writer) *logRouter {
	return &logRouter{sink: func(line string) { fmt.Fprintln(out, line) }}
}

func (r *logRouter) SetSink(sink func(line string)) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

func (r *logRouter) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink(line)
	}
}

func buildSession(opts *config.Options, logs *logRouter) *auth.Session {
	transport := httpTransport()
	client := &ardplus.Client{HTTP: &http.Client{Timeout: 120 * time.Second, Transport: transport}}
	client.Logf = logs.Logf
	return &auth.Session{
		Store:    auth.Store{Path: opts.TokenFile},
		Login:    &auth.Client{HTTP: &http.Client{Timeout: 120 * time.Second, Transport: transport}},
		Playback: client,
		Username: opts.Username,
		Password: opts.Password,
		Logf:     logs.Logf,
	}
}

// httpTransport returns the shared transport. The DASH downloader runs
// several workers against the same host, so the idle connection pool must be
// larger than the default of two connections per host.
func httpTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil
	}
	clone := base.Clone()
	clone.MaxIdleConns = 64
	clone.MaxIdleConnsPerHost = 16
	clone.IdleConnTimeout = 90 * time.Second
	clone.ForceAttemptHTTP2 = true
	return clone
}

// resolveDebugDir returns the directory for the diagnostic files, or "" when
// the feature is off. The directory is created so the components can write
// into it right away.
func resolveDebugDir(env config.Env) string {
	dir := env("ARD_PLUS_DEBUG_DIR")
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Warnung: Diagnose-Verzeichnis nicht anlegbar (%s): %v\n", dir, err)
		return ""
	}
	return dir
}

func buildApp(opts *config.Options, session *auth.Session, ffmpeg, ffprobe string, stdout io.Writer, logs *logRouter, state *store.Store, debugDir string) *app.App {
	transport := httpTransport()
	downloader := &dash.Downloader{HTTP: &http.Client{Transport: transport}, Logf: logs.Logf}
	muxer := &dash.Muxer{FFmpeg: ffmpeg, Logf: logs.Logf}
	pipeline := &app.Pipeline{
		// The manifest request needs a timeout of its own: without one a
		// stalled connection would block the run indefinitely.
		HTTP:       &http.Client{Timeout: 60 * time.Second, Transport: transport},
		Downloader: downloader,
		Muxer:      muxer,
		DebugDir:   debugDir,
		Logf:       logs.Logf,
	}
	if debugDir != "" {
		downloader.DebugDir = debugDir
	}
	manager := &download.Manager{
		DownloadsDir: opts.DownloadsDir,
		FileTag:      opts.FileTag,
		Force:        opts.ForceRedownload,
		Probe:        func(ctx context.Context, path string) (string, error) { return media.ProbeMediaTag(ctx, ffprobe, path) },
		Logf:         logs.Logf,
	}
	application := &app.App{
		Config:  opts,
		Session: session,
		API:     session.Playback.(*ardplus.Client),
		Fetcher: pipeline,
		Manager: manager,
		Store:   state,
	}
	application.Logf = logs.Logf
	// A non-interactive default: callers that own a terminal replace it.
	application.UI = &headlessUI{out: stdout, in: strings.NewReader("")}
	return application
}
