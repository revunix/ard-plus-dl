// Package app orchestrates the ARD Plus workflows: movie, series and Tatort
// downloads, including skip handling, failure accounting and batch mode.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/download"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/store"
)

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
		url = categoryURL("tatort-" + slug)
	}
	switch {
	case showPath == "tatort":
		// The archive itself is not stored: the selected categories are.
		return a.hubFlow(ctx)
	case strings.HasPrefix(showPath, "tatort-"):
		a.Remember(url, "Tatort "+capitalize(strings.TrimPrefix(showPath, "tatort-")), store.KindTatort)
		return a.tatortFlow(ctx, showPath)
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
		FileTag:     a.itemTag(),
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
		if err := ctx.Err(); err != nil {
			return err
		}
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
			if err := ctx.Err(); err != nil {
				return err
			}
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
				FileTag:     a.itemTag(),
			}
			if skipped, err := a.Manager.Prepare(item); err != nil {
				a.recordEpisodeFailure("could not prepare %s: %s", staging, err.Error())
				continue
			} else if skipped {
				if a.UI != nil {
					a.UI.Event(Event{Kind: EventSkipped, Label: staging})
				}
				a.markDone(episode.ID)
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

func (a *App) tatortFlow(ctx context.Context, slug string) error {
	city := strings.TrimPrefix(slug, "tatort-")
	urls, err := a.API.TatortEpisodes(ctx, slug)
	if err != nil {
		return err
	}
	a.logf("Der Tatort %s hat %d Episoden.", capitalize(city), len(urls))

	// The skip value of the command line is a series-only option; the Tatort
	// flow asks for it, and the automatic mode loads every episode.
	skip := 0
	if !a.automatic() {
		skip, err = a.UI.AskTatortSkip(ctx, capitalize(city), len(urls))
		if err != nil {
			return err
		}
	}
	a.logf("Überspringe %d Episode(n).", skip)
	if skip >= len(urls) {
		return nil
	}

	for _, listed := range urls[skip:] {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		// The glob must always be set. Without it an episode that carries no
		// production number would never be recognised as downloaded and would
		// be fetched again on every run.
		finalGlob := base + ".*"
		item := download.Item{
			StagingPath: staging,
			FinalGlob:   finalGlob,
			BaseStem:    base,
			FileTag:     a.itemTag(),
		}
		if skipped, err := a.Manager.Prepare(item); err != nil {
			a.recordEpisodeFailure("could not prepare %s: %s", staging, err.Error())
			continue
		} else if skipped {
			if a.UI != nil {
				a.UI.Event(Event{Kind: EventSkipped, Label: staging})
			}
			a.markDone(episodeID)
			continue
		}
		a.logf("Lade %s...", staging)
		if err := a.downloadItem(ctx, item, movie.ID, contentTypeMovie, movie.DashURL, staging, staging); err != nil {
			a.recordEpisodeFailure("%s", err.Error())
			continue
		}
		a.markDone(episodeID)
		// The pause keeps the requests to the API at a human pace. It gives
		// up when the run is cancelled.
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
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

// sleep waits for the given duration and gives up when the context is
// cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	// The playback session must also be released when the run is cancelled,
	// so the context is detached from the cancellation.
	defer a.Session.Release(context.WithoutCancel(ctx), contentID, contentType)

	if dashURL == "" {
		return a.fail("no video URL in API response for %s", staging)
	}
	manifestURL, err := buildManifestURL(dashURL, param)
	if err != nil {
		return a.fail("invalid video URL for %s: %s", staging, err.Error())
	}

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
	final, err := a.Manager.Finalize(ctx, item, stagingPath+".mp4")
	if err != nil {
		return err
	}
	if a.UI != nil {
		a.UI.Event(Event{Kind: EventDone, Label: label, Path: final})
	}
	return nil
}

// buildManifestURL adds the playback parameters to the manifest URL. The
// parameters are appended verbatim: they carry signatures whose characters
// must reach the CDN exactly as they were signed.
func buildManifestURL(dashURL, param string) (string, error) {
	if param == "" {
		return dashURL, nil
	}
	parsed, err := url.Parse(dashURL)
	if err != nil {
		return "", err
	}
	separator := "?"
	if parsed.RawQuery != "" {
		separator = "&"
	}
	return dashURL + separator + param, nil
}

// hubFlow processes the Tatort hub: the user picks the categories to work on.
func (a *App) hubFlow(ctx context.Context) error {
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
		a.Remember(categoryURL(slug), title, store.KindTatort)
		a.episodeFailures, a.firstFailure = 0, ""
		if err := a.tatortFlow(ctx, slug); err != nil {
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
