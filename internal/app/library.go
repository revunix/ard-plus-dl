package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// CheckResult reports the outcome of a new-episode check for one entry.
type CheckResult struct {
	Series store.Series
	Title  string
	Kind   string
	New    []store.EpisodeRef
	Total  int
	// Note describes a structural change instead of an episode count.
	Note string
	Err  error
}

// Library returns the stored entries.
func (a *App) Library() ([]store.Series, error) {
	if a.Store == nil {
		return nil, nil
	}
	return a.Store.SeriesList()
}

// CheckLibrary checks every enabled entry for episodes that were not seen
// before and stores the result.
func (a *App) CheckLibrary(ctx context.Context) ([]CheckResult, error) {
	var results []CheckResult
	checked := map[int64]bool{}
	// Two passes: entries created by an expansion (the Tatort archive) are
	// checked in the second one.
	for range 2 {
		entries, err := a.Library()
		if err != nil {
			return results, err
		}
		progressed := false
		for _, entry := range entries {
			if !entry.Enabled || checked[entry.ID] {
				continue
			}
			checked[entry.ID] = true
			progressed = true
			if err := ctx.Err(); err != nil {
				return results, err
			}
			results = append(results, a.CheckEntry(ctx, entry))
		}
		if !progressed {
			break
		}
	}
	return results, nil
}

// CheckEntry checks one library entry.
func (a *App) CheckEntry(ctx context.Context, entry store.Series) CheckResult {
	if isTatortHubURL(entry.URL) {
		_, result := a.ExpandHub(ctx, entry)
		return result
	}
	result := CheckResult{Series: entry}
	title, kind, refs, err := a.IndexEpisodes(ctx, entry.URL)
	if err != nil {
		result.Err = err
		if a.Store != nil {
			_ = a.Store.RecordCheck(entry.ID, 0, err.Error())
		}
		return result
	}
	result.Title = title
	if result.Title == "" {
		result.Title = entry.Title
	}
	if result.Title == "" && kind == store.KindTatort {
		slug := strings.TrimPrefix(lastPathSegment(entry.URL), "tatort-")
		result.Title = "Tatort " + capitalize(slug)
	}
	result.Kind = kind
	result.Total = len(refs)
	if a.Store == nil {
		return result
	}
	id, err := a.Store.UpsertSeries(entry.URL, title, kind)
	if err != nil {
		result.Err = err
		return result
	}
	fresh, err := a.Store.SyncEpisodes(id, refs)
	if err != nil {
		result.Err = err
		return result
	}
	result.New = fresh
	if err := a.Store.RecordCheck(id, len(fresh), ""); err != nil {
		result.Err = err
	}
	return result
}

// IndexEpisodes resolves an ARD Plus URL into the episodes the API currently
// reports. Movies have no episode list.
func (a *App) IndexEpisodes(ctx context.Context, url string) (title, kind string, refs []store.EpisodeRef, err error) {
	if err := config.ValidateContentURL(url); err != nil {
		return "", "", nil, err
	}
	slug := url
	if i := strings.LastIndexByte(slug, '/'); i >= 0 {
		slug = slug[i+1:]
	}

	if strings.HasPrefix(slug, "tatort-") {
		listed, listErr := a.API.TatortEpisodes(ctx, slug)
		if listErr != nil {
			return "", "", nil, listErr
		}
		for _, item := range listed {
			id := ardplus.DetailID(item.URL)
			if id == "" {
				continue
			}
			refs = append(refs, store.EpisodeRef{ID: id, Title: item.Name})
		}
		return "", store.KindTatort, refs, nil
	}

	showID := slug
	if i := strings.IndexByte(showID, '-'); i >= 0 {
		showID = showID[:i]
	}
	if showID == "" {
		return "", "", nil, errors.New("could not parse content ID from URL")
	}
	movie, series, err := a.API.MovieDetails(ctx, showID)
	if err != nil {
		return "", "", nil, err
	}
	if movie != nil {
		return movie.Title, store.KindMovie, nil, nil
	}
	if series == nil {
		return "", "", nil, errors.New("invalid content (no movie or series in API response)")
	}
	for _, season := range series.Seasons {
		episodes, listErr := a.API.Episodes(ctx, season.ID)
		if listErr != nil {
			return "", "", nil, listErr
		}
		for _, episode := range episodes {
			ref := store.EpisodeRef{ID: episode.ID, Title: episode.Title, Season: season.SeasonInSeries}
			if episode.EpisodeInSeason != nil {
				ref.Number = *episode.EpisodeInSeason
			}
			refs = append(refs, ref)
		}
	}
	return series.Title, store.KindSeries, refs, nil
}

// DownloadNew downloads the episodes of one entry that are known but not
// downloaded yet.
func (a *App) DownloadNew(ctx context.Context, entry store.Series) error {
	if a.Store == nil {
		return errors.New("no library storage")
	}
	if isTatortHubURL(entry.URL) {
		return a.downloadHub(ctx, entry)
	}
	pending, err := a.Store.Pending(entry.ID)
	if err != nil {
		return err
	}
	if len(pending) == 0 && entry.Kind == store.KindMovie {
		// A movie has no episode list; loading it again is the whole job.
		a.nonInteractive = true
		defer func() { a.nonInteractive = false }()
		a.applySeriesDefaults(entry)
		defer a.clearSeriesDefaults()
		return a.runURL(ctx, entry.URL, a.entrySkip(entry))
	}
	if len(pending) == 0 {
		return nil
	}
	return a.downloadSelection(ctx, entry, nil)
}

// DownloadEpisodes downloads exactly the given episodes of one entry; an empty
// selection means every pending episode.
func (a *App) DownloadEpisodes(ctx context.Context, entry store.Series, ids []string) error {
	if a.Store == nil {
		return errors.New("no library storage")
	}
	if len(ids) == 0 {
		return a.DownloadNew(ctx, entry)
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	return a.downloadSelection(ctx, entry, wanted)
}

// downloadSelection runs the flow of one entry restricted to wanted; a nil set
// means "every pending episode".
func (a *App) downloadSelection(ctx context.Context, entry store.Series, wanted map[string]bool) error {
	if wanted == nil {
		pending, err := a.Store.Pending(entry.ID)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		wanted = make(map[string]bool, len(pending))
		for _, episode := range pending {
			wanted[episode.ID] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	episodes, err := a.Store.Episodes(entry.ID)
	needed := map[int]int{}
	matched := 0
	if err == nil {
		for _, episode := range episodes {
			if wanted[episode.ID] && episode.Season > 0 {
				needed[episode.Season]++
				matched++
			}
		}
	}
	// Restrict the flow to the seasons of the requested episodes, but only
	// when every requested episode has a known season.
	if err == nil && matched == len(wanted) && len(needed) > 0 {
		a.onlySeasons = needed
	} else {
		a.onlySeasons = nil
	}

	seriesID := entry.ID
	a.onlyEpisodes = wanted
	a.nonInteractive = true
	a.applySeriesDefaults(entry)
	a.markDownloaded = func(episodeID, path string) { _ = a.Store.MarkDownloaded(seriesID, episodeID, path) }
	defer func() {
		a.onlyEpisodes = nil
		a.onlySeasons = nil
		a.markDownloaded = nil
		a.nonInteractive = false
		a.clearSeriesDefaults()
	}()

	a.logf("%d Folge(n) für %s.", len(wanted), entry.Title)
	return a.runURL(ctx, entry.URL, a.entrySkip(entry))
}

// applySeriesDefaults starts the run of a series with its own values.
func (a *App) applySeriesDefaults(store.Series) {
	a.skip = 0
}

// clearSeriesDefaults drops the per-series values again.
func (a *App) clearSeriesDefaults() {
	a.skip = 0
}

// entrySkip returns the number of leading episodes of the first season that
// are skipped for the entry. A value of 0 or 1 skips nothing.
func (a *App) entrySkip(entry store.Series) int {
	if entry.Skip > 1 {
		return entry.Skip
	}
	return 1
}

// downloadHub replaces the archive entry with its categories and downloads the
// episodes of the newly created ones.
func (a *App) downloadHub(ctx context.Context, entry store.Series) error {
	created, result := a.ExpandHub(ctx, entry)
	if result.Err != nil {
		return result.Err
	}
	failed := 0
	for _, category := range created {
		check := a.CheckEntry(ctx, category)
		if check.Err != nil {
			a.logf("FEHLER: %s: %s", category.Title, check.Err)
			failed++
			continue
		}
		if err := a.downloadSelection(ctx, category, nil); err != nil {
			a.logf("FEHLER: %s: %s", category.Title, err)
			failed++
		}
	}
	if failed > 0 {
		return a.fail("%d Tatort-Kategorie(n) fehlgeschlagen", failed)
	}
	return nil
}

// ExpandHub turns the Tatort archive entry into one entry per category and
// removes the archive entry itself.
func (a *App) ExpandHub(ctx context.Context, entry store.Series) ([]store.Series, CheckResult) {
	result := CheckResult{Series: entry, Title: entry.Title, Kind: store.KindTatort}
	if a.Store == nil {
		result.Err = errors.New("no library storage")
		return nil, result
	}
	categories, err := a.API.TatortCategories(ctx)
	if err != nil {
		result.Err = err
		_ = a.Store.RecordCheck(entry.ID, 0, err.Error())
		return nil, result
	}

	var created []store.Series
	for _, category := range categories {
		url := categoryURL(category.Slug)
		if _, err := a.Store.UpsertSeries(url, category.Title, store.KindTatort); err != nil {
			result.Err = err
			return created, result
		}
		if stored, err := a.Store.SeriesByURL(url); err == nil && stored != nil {
			created = append(created, *stored)
		}
	}
	if err := a.Store.RemoveSeries(entry.ID); err != nil {
		result.Err = err
		return created, result
	}
	result.Note = fmt.Sprintf("in %d Kategorie-Einträge aufgeteilt", len(created))
	result.Title = entry.Title
	if result.Title == "" {
		result.Title = entry.URL
	}
	return created, result
}

// lastPathSegment returns the final URL path segment.
func lastPathSegment(url string) string {
	trimmed := strings.TrimSuffix(url, "/")
	if index := strings.LastIndexByte(trimmed, '/'); index >= 0 {
		return trimmed[index+1:]
	}
	return trimmed
}

// isTatortHubURL reports whether a URL points at the Tatort archive page.
func isTatortHubURL(url string) bool {
	trimmed := strings.TrimSuffix(url, "/")
	return strings.HasSuffix(trimmed, "/tatort")
}

// Remember stores the URL of a run together with the metadata the run learned.
func (a *App) Remember(url, title, kind string) {
	if a.Store == nil || url == "" {
		return
	}
	if _, err := a.Store.UpsertSeries(url, title, kind); err != nil {
		a.logf("Bibliothek konnte nicht aktualisiert werden: %v", err)
		return
	}
	if err := a.Store.SetSetting(store.SettingLastURL, url); err != nil {
		a.logf("letzte URL konnte nicht gespeichert werden: %v", err)
	}
}
