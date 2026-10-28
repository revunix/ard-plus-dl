// Package cli is the headless entry point: argument handling, dependency
// checks, wiring and exit codes.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/store"
)

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
		if opts.JSON {
			return printLibraryJSON(stdout, entries)
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
	// ffmpeg and a writable downloads directory.
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

// libraryEntryJSON is one entry of the library in the machine readable form.
type libraryEntryJSON struct {
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
	Skip     int    `json:"skip"`
	NewCount int    `json:"new_count"`
	AddedAt  string `json:"added_at"`
	Checked  string `json:"checked_at,omitempty"`
	LastErr  string `json:"last_error,omitempty"`
}

// printLibraryJSON writes the library as JSON for scripts.
func printLibraryJSON(w io.Writer, entries []store.Series) int {
	list := make([]libraryEntryJSON, 0, len(entries))
	for _, entry := range entries {
		item := libraryEntryJSON{
			ID:       entry.ID,
			URL:      entry.URL,
			Title:    entry.Title,
			Kind:     entry.Kind,
			Enabled:  entry.Enabled,
			Skip:     entry.Skip,
			NewCount: entry.NewCount,
			LastErr:  entry.LastError,
		}
		if !entry.AddedAt.IsZero() {
			item.AddedAt = entry.AddedAt.Local().Format(time.RFC3339)
		}
		if entry.CheckedAt != nil {
			item.Checked = entry.CheckedAt.Local().Format(time.RFC3339)
		}
		list = append(list, item)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(list); err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	return 0
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
