// Package app orchestrates the ARD Plus workflows: movie, series and Tatort
// downloads, including skip handling, failure accounting and batch mode.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/auth"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/dash"
	"github.com/revunix/ard-plus-dl/internal/download"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// categoryURL returns the canonical ARD Plus URL of a Tatort category. The
// library always stores this form, no matter which base address the API
// client uses for its requests.
func categoryURL(slug string) string {
	return "https://www.ardplus.de/kategorie/" + slug
}

// Content types used by the playback API.
const (
	contentTypeMovie   = "CmsMovie"
	contentTypeEpisode = "CmsEpisode"
)

// Season is one selectable season of a series.
type Season struct {
	Number int
	Title  string
	ID     string
}

// Category is one selectable Tatort category.
type Category struct {
	Slug  string
	Title string
}

// EventKind classifies progress events for the UI.
type EventKind int

// Event kinds.
const (
	EventLog EventKind = iota
	EventQueued
	EventProgress
	EventSkipped
	EventDone
	EventFailed
)

// Event is one user-visible state change.
type Event struct {
	Kind    EventKind
	Label   string
	Path    string
	Message string
	Done    int
	Total   int
	// EpisodeID names the episode the event belongs to.
	EpisodeID string
	// SeriesID names the library entry of the episode.
	SeriesID int64
}

// UI is the front end the app talks to.
type UI interface {
	Start(ctx context.Context, cfg *config.Options) error
	SelectSeasons(ctx context.Context, show string, seasons []Season) ([]int, error)
	SelectCategories(ctx context.Context, title string, categories []Category) ([]string, error)
	AskTatortSkip(ctx context.Context, city string, count int) (int, error)
	Event(Event)
}

// Fetcher downloads and muxes one manifest.
type Fetcher interface {
	Fetch(ctx context.Context, manifestURL, workDir string, progress func(done, total int)) ([]dash.DownloadedTrack, error)
	Mux(ctx context.Context, workDir string, tracks []dash.DownloadedTrack, out string) error
}

// App runs the download workflows.
type App struct {
	Config  *config.Options
	Session *auth.Session
	API     *ardplus.Client
	Fetcher Fetcher
	Manager *download.Manager
	UI      UI
	Logf    func(format string, args ...any)

	// Store, when set, keeps the library and the settings.
	Store *store.Store

	// onlyEpisodes, when set, restricts the series and Tatort flows to these
	// episode IDs; markDownloaded records finished episodes in the library.
	onlyEpisodes   map[string]bool
	markDownloaded func(episodeID, path string)
	// onlySeasons, when set for a selection, holds the requested episodes per
	// season number so the flow skips seasons without requested episodes.
	onlySeasons map[int]int
	// fileTag is the Datei-Tag of the series that is currently downloaded. It
	// overrides the global tag when the series carries its own.
	fileTag string
	// skip is the number of leading episodes of the first season that are
	// skipped for the series that is currently downloaded.
	skip int
	// nonInteractive suppresses prompts for runs driven by a fixed selection
	// (--download-new).
	nonInteractive bool

	episodeFailures int
	firstFailure    string
	batchFailures   int
	logFile         *os.File
}

// automatic reports whether the flow must run without asking the user.
func (a *App) automatic() bool {
	return a.Config.Automatic || a.nonInteractive || a.onlyEpisodes != nil
}

// wanted reports whether an episode is part of the current selection.
func (a *App) wanted(episodeID string) bool {
	if a.onlyEpisodes == nil {
		return true
	}
	return a.onlyEpisodes[episodeID]
}

// itemTag returns the Datei-Tag that the items of the current run carry.
func (a *App) itemTag() string {
	return a.fileTag
}

// markDone records a downloaded episode in the library. The path is the file
// the episode ended up in, empty when it was already there.
func (a *App) markDone(episodeID, path string) {
	if a.markDownloaded != nil {
		a.markDownloaded(episodeID, path)
	}
}

func (a *App) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if a.Logf != nil {
		a.Logf("%s", line)
	}
	if a.logFile != nil {
		_, _ = a.logFile.WriteString(line + "\n")
	}
	if a.UI != nil {
		a.UI.Event(Event{Kind: EventLog, Message: line})
	}
}

func (a *App) fail(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// Run executes the configured mode: one URL or a links file.
func (a *App) Run(ctx context.Context) error {
	if a.UI != nil {
		if err := a.UI.Start(ctx, a.Config); err != nil {
			return err
		}
	}
	if err := a.Session.Ensure(ctx); err != nil {
		return err
	}
	a.logf("Downloads directory: %s", a.Manager.DownloadsDir)

	if a.Config.Batch() {
		return a.runBatch(ctx)
	}
	if err := a.runURL(ctx, a.Config.URL, a.Config.Skip); err != nil {
		return err
	}
	return nil
}

func (a *App) runBatch(ctx context.Context) error {
	links, err := readLinks(a.Config.LinksFile)
	if err != nil {
		return err
	}
	stamp := time.Now().Format("2006-01-02_15-04-05")
	logsDir := filepath.Join(filepath.Dir(a.Config.LinksFile), "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("Logs directory is not writable: %s", logsDir)
	}
	successLog := filepath.Join(logsDir, "successful_links_"+stamp+".txt")
	failedLog := filepath.Join(logsDir, "failed_links_"+stamp+".txt")
	downloadLog := filepath.Join(logsDir, "download_log_"+stamp+".txt")

	for _, path := range []string{successLog, failedLog, downloadLog} {
		file, createErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if createErr != nil {
			return fmt.Errorf("Logs directory is not writable: %s", logsDir)
		}
		file.Close()
	}
	logFile, err := os.OpenFile(downloadLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("Logs directory is not writable: %s", logsDir)
	}
	defer func() {
		logFile.Close()
		a.logFile = nil
	}()
	a.logFile = logFile

	a.logf("Batch download started at %s", stamp)
	a.logf("Links file: %s", a.Config.LinksFile)
	a.logf("Download log: %s", downloadLog)

	for _, link := range links {
		url := media.NormalizeURL(link)
		a.logf("Processing: %s", url)
		err := a.runURL(ctx, url, a.Config.Skip)
		if err == nil {
			appendLine(successLog, url)
			a.logf("Recorded success: %s", url)
		} else {
			reason := err.Error()
			appendLine(failedLog, url+"\t"+reason)
			a.logf("Recorded failure: %s (%s)", url, reason)
			a.batchFailures++
		}
	}
	a.logf("Batch download finished.")
	a.logf("Successful links: %s", successLog)
	a.logf("Failed links: %s", failedLog)
	if a.batchFailures > 0 {
		return fmt.Errorf("%d link(s) failed", a.batchFailures)
	}
	return nil
}

func readLinks(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Links file missing or not found: %s", path)
	}
	var links []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		links = append(links, line)
	}
	return links, nil
}

func appendLine(path, line string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}
