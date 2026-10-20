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
	"os/exec"
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

// runLibraryMode handles --library, --add, --check and --download-new.
func runLibraryMode(ctx context.Context, opts *config.Options, env config.Env, stdout, stderr io.Writer, state *store.Store, interactive bool, stdin io.Reader, stdinTTY bool) int {
	if state == nil {
		fmt.Fprintln(stderr, "Bibliothek nicht verfügbar.")
		return 1
	}

	switch opts.LibraryMode {
	case "list":
		entries, err := state.SeriesList()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printLibrary(stdout, entries)
		return 0

	case "add":
		url := media.NormalizeURL(opts.AddURL)
		if err := config.ValidateContentURL(url); err != nil {
			fmt.Fprintf(stderr, "Invalid URL: %s\n", err)
			return 1
		}
		if _, err := state.UpsertSeries(url, "", ""); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Zur Bibliothek hinzugefügt: %s\n", url)
		return 0
	}

	// --check and --download-new need a session; downloading additionally needs
	// ffmpeg, a tag and a writable downloads directory.
	if entries, err := state.SeriesList(); err == nil && len(entries) == 0 {
		fmt.Fprintln(stdout, "Bibliothek ist leer. Mit --add <url> eine Serie hinzufügen.")
		return 0
	}

	ffmpeg, ffprobe := "", ""
	if opts.LibraryMode == "download-new" {
		var err error
		ffmpeg, ffprobe, err = checkDependencies(env)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if opts.FileTag == "" && stdinTTY {
			fmt.Fprint(stdout, "Datei-Tag (z. B. example): ")
			opts.FileTag = readLine(ctx, stdin)
		}
		if err := config.ValidateFileTag(opts.FileTag); err != nil {
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
	}

	opts.TokenFile = config.TokenPath(env, opts.WorkDir)
	if !fileExists(opts.TokenFile) && (opts.Username == "" || opts.Password == "") && !interactive {
		fmt.Fprint(stderr, credentialsHelp(opts))
		return 1
	}

	logs := newLogRouter(stdout)
	session := buildSession(opts, logs)
	if err := session.Ensure(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	debugDir := resolveDebugDir(env)
	application := buildApp(opts, session, ffmpeg, ffprobe, stdout, logs, state, debugDir)
	if client, ok := session.Playback.(*ardplus.Client); ok && debugDir != "" {
		client.Debugf = logs.Logf
	}

	results, err := application.CheckLibrary(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	failed := printCheckResults(stdout, results)

	if opts.LibraryMode == "download-new" {
		for _, result := range results {
			if result.Err != nil {
				continue
			}
			// DownloadNew works off the pending set: an episode that was known
			// but not downloaded yet is retried too.
			pending, countErr := state.CountPending(result.Series.ID)
			if countErr != nil {
				fmt.Fprintf(stderr, "%s: %v\n", displayTitle(result), countErr)
				failed = true
				continue
			}
			if pending == 0 {
				continue
			}
			if err := application.DownloadNew(ctx, result.Series); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", displayTitle(result), err)
				failed = true
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func displayTitle(result app.CheckResult) string {
	if result.Title != "" {
		return result.Title
	}
	return result.Series.URL
}

func printLibrary(w io.Writer, entries []store.Series) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "Bibliothek ist leer. Mit --add <url> eine Serie hinzufügen.")
		return
	}
	for _, entry := range entries {
		state := ""
		if !entry.Enabled {
			state = ", deaktiviert"
		}
		checked := "noch nicht geprüft"
		if entry.CheckedAt != nil {
			checked = "geprüft " + entry.CheckedAt.Local().Format("2006-01-02 15:04")
		}
		title := entry.Title
		if title == "" {
			title = entry.URL
		}
		fmt.Fprintf(w, "%s [%s] %d neu, %s%s\n", title, kindLabel(entry.Kind), entry.NewCount, checked, state)
	}
}

func printCheckResults(w io.Writer, results []app.CheckResult) bool {
	failed := false
	newEpisodes := 0
	for _, result := range results {
		name := displayTitle(result)
		if result.Err != nil {
			fmt.Fprintf(w, "%s: Fehler: %v\n", name, result.Err)
			failed = true
			continue
		}
		if result.Note != "" {
			fmt.Fprintf(w, "%s: %s\n", name, result.Note)
			continue
		}
		switch result.Kind {
		case store.KindMovie:
			fmt.Fprintf(w, "%s: Film\n", name)
		default:
			fmt.Fprintf(w, "%s: %d neue Folge(n), %d insgesamt\n", name, len(result.New), result.Total)
			newEpisodes += len(result.New)
		}
	}
	fmt.Fprintf(w, "%d Serie(n) geprüft, %d neue Folge(n).\n", len(results), newEpisodes)
	return failed
}

func kindLabel(kind string) string {
	switch kind {
	case store.KindSeries:
		return "Serie"
	case store.KindTatort:
		return "Tatort"
	case store.KindMovie:
		return "Film"
	}
	return "unbekannt"
}

func useTUI(opts *config.Options, stdinTTY, stdoutTTY bool) bool {
	return stdinTTY && stdoutTTY && !opts.Automatic && !opts.Batch()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// readLine reads one line from the reader. It gives up when the context is
// cancelled, so that Ctrl+C does not leave the process in a prompt.
func readLine(ctx context.Context, reader io.Reader) string {
	buf := make([]byte, 0, 64)
	one := make([]byte, 1)
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ""
			default:
			}
		}
		n, err := reader.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(buf))
}

func promptCredentials(ctx context.Context, opts *config.Options, stdin io.Reader, stdout io.Writer, stdinFile *os.File) {
	if opts.Username == "" {
		fmt.Fprint(stdout, "ARD Plus username: ")
		opts.Username = readLine(ctx, stdin)
	}
	if opts.Password == "" {
		fmt.Fprint(stdout, "ARD Plus password: ")
		if stdinFile != nil {
			if data, err := term.ReadPassword(int(stdinFile.Fd())); err == nil {
				opts.Password = string(data)
				fmt.Fprintln(stdout)
				return
			}
		}
		opts.Password = readLine(ctx, stdin)
	}
}

func credentialsHelp(opts *config.Options) string {
	return "Credentials missing! Provide them via one of:\n" +
		"  - environment variables: ARD_PLUS_USER and ARD_PLUS_PASSWORD\n" +
		"  - credentials file: " + opts.CredentialsFile + " (chmod 600, lines: username=... / password=...)\n" +
		"  - positional arguments (visible to other processes via ps - not recommended)\n"
}

func resolveWorkDir(opts *config.Options) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if !opts.Batch() {
		return cwd, nil
	}
	abs, err := filepath.Abs(opts.LinksFile)
	if err != nil {
		return "", err
	}
	opts.LinksFile = abs
	return filepath.Dir(abs), nil
}

func ensureWritableDir(path string) bool {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	probe := filepath.Join(path, ".ard-plus-dl-write-test")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	file.Close()
	_ = os.Remove(probe)
	return true
}

func checkDependencies(env config.Env) (ffmpeg, ffprobe string, err error) {
	ffmpeg = env("ARD_PLUS_FFMPEG")
	if ffmpeg == "" {
		ffmpeg, _ = exec.LookPath("ffmpeg")
	}
	ffprobe = env("ARD_PLUS_FFPROBE")
	if ffprobe == "" {
		ffprobe, _ = exec.LookPath("ffprobe")
	}
	var missing []string
	if ffmpeg == "" {
		missing = append(missing, "ffmpeg")
	}
	if ffprobe == "" {
		missing = append(missing, "ffprobe")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("Missing required dependencies: %s", strings.Join(missing, " "))
	}
	return ffmpeg, ffprobe, nil
}

// headlessUI prints plain lines and asks for the same answers the Bash
// implementation asked for.
type headlessUI struct {
	out         io.Writer
	in          io.Reader
	interactive bool
}

func (u *headlessUI) Start(context.Context, *config.Options) error { return nil }

func (u *headlessUI) Event(event app.Event) {
	if event.Kind == app.EventFailed && event.Message != "" {
		fmt.Fprintf(u.out, "%s\n", event.Message)
	}
}

func (u *headlessUI) SelectSeasons(ctx context.Context, show string, seasons []app.Season) ([]int, error) {
	if !u.interactive {
		return nil, errors.New("no season selected (input ended)")
	}
	for _, season := range seasons {
		fmt.Fprintf(u.out, "%5d\t%s\n", season.Number, season.Title)
	}
	for {
		fmt.Fprintf(u.out, "Welche Staffel möchtest du runterladen? ")
		raw := readLine(ctx, u.in)
		if raw == "" {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("no season selected (input ended)")
		}
		var selected []int
		valid := true
		for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
			number, err := parseSeasonNumber(field)
			if err != nil || !hasSeason(seasons, number) {
				valid = false
				break
			}
			selected = append(selected, number)
		}
		if valid && len(selected) > 0 {
			return selected, nil
		}
		fmt.Fprintln(u.out, "Ungültige Staffelauswahl, bitte erneut versuchen.")
	}
}

func (u *headlessUI) SelectCategories(ctx context.Context, title string, categories []app.Category) ([]string, error) {
	if !u.interactive {
		return nil, errors.New("no category selected (input ended)")
	}
	fmt.Fprintf(u.out, "%s:\n", title)
	for i, category := range categories {
		fmt.Fprintf(u.out, "%4d\t%s\n", i+1, category.Title)
	}
	for {
		fmt.Fprintf(u.out, "Welche Kategorien? (Nummern, 'alle') ")
		raw := strings.ToLower(readLine(ctx, u.in))
		if raw == "" {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("no category selected (input ended)")
		}
		if raw == "alle" || raw == "all" {
			slugs := make([]string, 0, len(categories))
			for _, category := range categories {
				slugs = append(slugs, category.Slug)
			}
			return slugs, nil
		}
		var slugs []string
		valid := true
		for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
			number, err := parseSeasonNumber(field)
			if err != nil || number < 1 || number > len(categories) {
				valid = false
				break
			}
			slugs = append(slugs, categories[number-1].Slug)
		}
		if valid && len(slugs) > 0 {
			return slugs, nil
		}
		fmt.Fprintln(u.out, "Ungültige Auswahl, bitte erneut versuchen.")
	}
}

func (u *headlessUI) AskTatortSkip(ctx context.Context, _ string, _ int) (int, error) {
	if !u.interactive {
		return 0, nil
	}
	fmt.Fprintf(u.out, "Wie viele Episoden möchtest du überspringen? (0=alle laden) ")
	raw := readLine(ctx, u.in)
	if raw == "" {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	number, err := parseSeasonNumber(raw)
	if err != nil {
		return 0, nil
	}
	return number, nil
}

func parseSeasonNumber(field string) (int, error) {
	if field == "" {
		return 0, errors.New("empty")
	}
	number := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		number = number*10 + int(r-'0')
	}
	return number, nil
}

func hasSeason(seasons []app.Season, number int) bool {
	for _, season := range seasons {
		if season.Number == number {
			return true
		}
	}
	return false
}
