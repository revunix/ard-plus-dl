// Package app orchestrates the ARD Plus workflows: movie, series and Tatort
// downloads, including skip handling, failure accounting and batch mode.
package app

import (
	"context"
	"errors"
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
	Err     error
	Done    int
	Total   int
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
	markDownloaded func(episodeID string)
	// onlySeasons, when set for a selection, holds the requested episodes per
	// season number so the flow skips seasons without requested episodes.
	onlySeasons map[int]int
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

// markDone records a downloaded episode in the library.
func (a *App) markDone(episodeID string) {
	if a.markDownloaded != nil {
		a.markDownloaded(episodeID)
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
	return errors.New(fmt.Sprintf(format, args...))
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

// runURL dispatches one ARD Plus URL.
func (a *App) runURL(ctx context.Context, url string, skip int) error {
	a.episodeFailures = 0
	a.firstFailure = ""

	if err := config.ValidateContentURL(url); err != nil {
		return a.fail("invalid URL: %s", err.Error())
	}
	showPath := url
	if i := strings.LastIndexByte(showPath, '/'); i >= 0 {
		showPath = showPath[i+1:]
	}
	// /tatort-<stadt> is the short form of the category page.
	if slug, ok := strings.CutPrefix(showPath, "tatort-"); ok && !strings.Contains(url, "/kategorie/") {
		url = a.API.CategoryPageURL("tatort-" + slug)
	}
	switch {
	case showPath == "tatort":
		// The archive itself is not stored: the selected categories are.
		return a.hubFlow(ctx, skip)
	case strings.HasPrefix(showPath, "tatort-"):
		a.Remember(url, "Tatort "+capitalize(strings.TrimPrefix(showPath, "tatort-")), store.KindTatort)
		return a.tatortFlow(ctx, showPath, skip)
	}

	showID := showPath
	if i := strings.IndexByte(showID, '-'); i >= 0 {
		showID = showID[:i]
	}
	if showID == "" {
		return errors.New("could not parse content ID from URL")
	}

	movie, series, err := a.API.MovieDetails(ctx, showID)
	if err != nil {
		return a.fail("could not fetch content metadata (graphql: %s)", err.Error())
	}
	switch {
	case movie != nil:
		a.Remember(url, movie.Title, store.KindMovie)
		return a.movieFlow(ctx, movie)
	case series != nil:
		a.Remember(url, series.Title, store.KindSeries)
		return a.seriesFlow(ctx, series, skip)
	default:
		return errors.New("invalid content (no movie or series in API response)")
	}
}

func (a *App) movieFlow(ctx context.Context, movie *ardplus.Movie) error {
	name := movie.Title
	staging := media.BuildMoviePath(name, movie.ProductionYear)
	stem := media.MovieNameStem(name, movie.ProductionYear)
	item := download.Item{
		StagingPath: staging,
		FinalGlob:   stem + "/" + stem + ".*",
		BaseStem:    stem,
		DirStem:     stem,
	}
	if skip, err := a.Manager.Prepare(item); skip || err != nil {
		return err
	}
	a.logf("Lade Film %s...", staging)
	if err := a.downloadItem(ctx, item, movie.ID, contentTypeMovie, movie.DashURL, staging, name); err != nil {
		return err
	}
	return nil
}

func (a *App) seriesFlow(ctx context.Context, series *ardplus.Series, skip int) error {
	seasons := make([]Season, 0, len(series.Seasons))
	for _, s := range series.Seasons {
		seasons = append(seasons, Season{Number: s.SeasonInSeries, Title: s.Title, ID: s.ID})
	}
	a.logf("Gewünschte Serie: %s", series.Title)

	var selected []int
	if a.automatic() {
		for _, s := range seasons {
			selected = append(selected, s.Number)
		}
	} else {
		var err error
		selected, err = a.UI.SelectSeasons(ctx, series.Title, seasons)
		if err != nil {
			return err
		}
	}

	applySkip := true
	for _, seasonNumber := range selected {
		seasonID := ""
		for _, s := range seasons {
			if s.Number == seasonNumber {
				seasonID = s.ID
				break
			}
		}
		count := 1
		if a.onlySeasons != nil {
			requested, ok := a.onlySeasons[seasonNumber]
			if !ok {
				continue
			}
			count = requested
		}
		if seasonID == "" {
			a.recordEpisodeFailures(count, "season %d not found", seasonNumber)
			continue
		}
		episodes, err := a.API.Episodes(ctx, seasonID)
		if err != nil {
			a.recordEpisodeFailures(count, "could not fetch episodes for season %d (graphql: %s)", seasonNumber, err.Error())
			continue
		}
		if len(episodes) == 0 {
			a.recordEpisodeFailures(count, "no episodes returned for season %d", seasonNumber)
			continue
		}
		a.logf("Staffel %d hat %d Folgen.", seasonNumber, len(episodes))

		maxEpisode := 0
		for _, episode := range episodes {
			if episode.EpisodeInSeason != nil && *episode.EpisodeInSeason > maxEpisode {
				maxEpisode = *episode.EpisodeInSeason
			}
		}
		padding := media.EpisodePadding(maxEpisode)

		start := 0
		if skip != 1 && applySkip {
			a.logf("Überspringe %d Episode(n) in der ersten Staffel.", skip)
			start = skip
			applySkip = false
		}
		if start >= len(episodes) {
			continue
		}
		for _, episode := range episodes[start:] {
			if !a.wanted(episode.ID) {
				continue
			}
			title := episode.Title
			staging := media.BuildEpisodePath(series.Title, seasonNumber, episode.EpisodeInSeason, title)
			episodeStem := media.EpisodeNameStem(series.Title, seasonNumber, episode.EpisodeInSeason, padding)
			seasonStem := media.SeasonNameStem(series.Title, seasonNumber)
			item := download.Item{
				StagingPath: staging,
				FinalGlob:   seasonStem + ".*/" + episodeStem + ".*",
				BaseStem:    episodeStem,
				DirStem:     seasonStem,
				DirUsesTag:  true,
			}
			if skipped, err := a.Manager.Prepare(item); skipped || err != nil {
				if skipped {
					if a.UI != nil {
						a.UI.Event(Event{Kind: EventSkipped, Label: staging})
					}
					a.markDone(episode.ID)
				}
				continue
			}
			a.logf("Lade %s...", staging)
			if err := a.downloadItem(ctx, item, episode.ID, contentTypeEpisode, episode.DashURL, staging, staging); err != nil {
				a.recordEpisodeFailure("%s", err.Error())
				continue
			}
			a.markDone(episode.ID)
		}
	}
	if a.episodeFailures > 0 {
		return a.fail("%d episode download(s) failed (first: %s)", a.episodeFailures, a.firstFailure)
	}
	return nil
}

func (a *App) tatortFlow(ctx context.Context, slug string, skip int) error {
	city := strings.TrimPrefix(slug, "tatort-")
	urls, err := a.API.TatortEpisodes(ctx, slug)
	if err != nil {
		return err
	}
	a.logf("Der Tatort %s hat %d Episoden.", capitalize(city), len(urls))

	if !a.automatic() {
		skip, err = a.UI.AskTatortSkip(ctx, capitalize(city), len(urls))
		if err != nil {
			return err
		}
	} else {
		// The CLI skip value is a series-only option; automatic mode loads
		// every episode of the category.
		skip = 0
	}
	a.logf("Überspringe %d Episode(n).", skip)
	if skip >= len(urls) {
		return nil
	}

	for _, listed := range urls[skip:] {
		episodeURL := listed.URL
		episodeID := ardplus.DetailID(episodeURL)
		if episodeID == "" {
			a.recordEpisodeFailure("could not parse Tatort episode id from %s", episodeURL)
			continue
		}
		// Skip unwanted episodes before asking the API for their details.
		if !a.wanted(episodeID) {
			continue
		}
		movie, _, err := a.API.MovieDetails(ctx, episodeID)
		if err != nil {
			a.recordEpisodeFailure("could not fetch Tatort episode details (graphql: %s)", err.Error())
			continue
		}
		if movie == nil || movie.ID == "" {
			a.recordEpisodeFailure("no movie metadata for Tatort episode %s", episodeID)
			continue
		}
		staging := media.BuildTatortPath(movie.CustomData.Location, movie.CustomData.Team,
			movie.CustomData.EpisodeProductionNumber, movie.Title, movie.ProductionYear)
		base := media.TatortNameStem(pick(movie.CustomData.Location, city), movie.CustomData.EpisodeProductionNumber, movie.ProductionYear)
		finalGlob := ""
		if movie.CustomData.EpisodeProductionNumber != "" {
			finalGlob = base + ".*"
		}
		item := download.Item{
			StagingPath: staging,
			FinalGlob:   finalGlob,
			BaseStem:    base,
		}
		if skipped, err := a.Manager.Prepare(item); skipped || err != nil {
			if skipped {
				if a.UI != nil {
					a.UI.Event(Event{Kind: EventSkipped, Label: staging})
				}
				a.markDone(episodeID)
			}
			continue
		}
		a.logf("Lade %s...", staging)
		if err := a.downloadItem(ctx, item, movie.ID, contentTypeMovie, movie.DashURL, staging, staging); err != nil {
			a.recordEpisodeFailure("%s", err.Error())
			continue
		}
		a.markDone(episodeID)
		time.Sleep(time.Second)
	}
	if a.episodeFailures > 0 {
		return a.fail("%d episode download(s) failed (first: %s)", a.episodeFailures, a.firstFailure)
	}
	return nil
}

func pick(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func capitalize(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (a *App) recordEpisodeFailure(format string, args ...any) {
	a.recordEpisodeFailures(1, format, args...)
}

func (a *App) recordEpisodeFailures(count int, format string, args ...any) {
	reason := fmt.Sprintf(format, args...)
	a.episodeFailures += count
	if a.firstFailure == "" {
		a.firstFailure = reason
	}
	a.logf("FEHLER: %s", reason)
	if a.UI != nil {
		a.UI.Event(Event{Kind: EventFailed, Message: reason})
	}
}

// downloadItem authorizes, downloads, muxes and renames one item.
func (a *App) downloadItem(ctx context.Context, item download.Item, contentID, contentType, dashURL, staging, label string) error {
	if a.UI != nil {
		a.UI.Event(Event{Kind: EventQueued, Label: label, Path: a.Manager.StagingPath(item)})
	}
	param, err := a.Session.Authorization(ctx, contentID, contentType)
	if err != nil {
		return a.fail("no playback authorization for %s: %s", staging, err.Error())
	}
	defer a.Session.Release(ctx, contentID, contentType)

	if dashURL == "" {
		return a.fail("no video URL in API response for %s", staging)
	}
	manifestURL := dashURL + "?" + param

	workDir := a.Manager.WorkDir(item)
	tracks, err := a.Fetcher.Fetch(ctx, manifestURL, workDir, func(done, total int) {
		if a.UI != nil {
			a.UI.Event(Event{Kind: EventProgress, Label: label, Done: done, Total: total})
		}
	})
	if err != nil {
		return a.fail("download failed for %s: %s", staging, err.Error())
	}
	stagingPath := a.Manager.StagingPath(item)
	if err := a.Fetcher.Mux(ctx, workDir, tracks, stagingPath); err != nil {
		return a.fail("muxing failed for %s: %s", staging, err.Error())
	}
	final, err := a.Manager.Finalize(item, stagingPath+".mp4")
	if err != nil {
		return err
	}
	if a.UI != nil {
		a.UI.Event(Event{Kind: EventDone, Label: label, Path: final})
	}
	return nil
}

// hubFlow processes the Tatort hub: the user picks the categories to work on.
func (a *App) hubFlow(ctx context.Context, skip int) error {
	categories, err := a.API.TatortCategories(ctx)
	if err != nil {
		return err
	}
	selected, err := a.selectCategories(ctx, categories)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return errors.New("no category selected (input ended)")
	}
	a.logf("Tatort: %d von %d Kategorie(n) ausgewählt.", len(selected), len(categories))

	titles := make(map[string]string, len(categories))
	for _, category := range categories {
		titles[category.Slug] = category.Title
	}

	failed := 0
	for _, slug := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		title := titles[slug]
		if title == "" {
			title = "Tatort " + capitalize(strings.TrimPrefix(slug, "tatort-"))
		}
		a.Remember(a.API.CategoryPageURL(slug), title, store.KindTatort)
		a.episodeFailures, a.firstFailure = 0, ""
		if err := a.tatortFlow(ctx, slug, skip); err != nil {
			a.logf("FEHLER: %s: %s", capitalize(strings.TrimPrefix(slug, "tatort-")), err.Error())
			failed++
		}
	}
	if failed > 0 {
		return a.fail("%d Tatort-Kategorie(n) fehlgeschlagen", failed)
	}
	return nil
}

// selectCategories asks for the categories to process. Without a terminal, or
// with --automatic, every category is selected.
func (a *App) selectCategories(ctx context.Context, categories []ardplus.TatortCategory) ([]string, error) {
	if a.automatic() {
		slugs := make([]string, 0, len(categories))
		for _, category := range categories {
			slugs = append(slugs, category.Slug)
		}
		return slugs, nil
	}
	items := make([]Category, 0, len(categories))
	for _, category := range categories {
		items = append(items, Category{Slug: category.Slug, Title: category.Title})
	}
	return a.UI.SelectCategories(ctx, "Tatort-Archiv", items)
}
