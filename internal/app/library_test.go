package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// newLibraryHarness builds the standard harness against a fresh SQLite
// database, so the library methods can be exercised end to end.
func newLibraryHarness(t *testing.T) (*harness, *store.Store) {
	t.Helper()
	opts := &config.Options{Automatic: true, Skip: 1, FileTag: "example"}
	h := newHarness(t, opts)
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("opening library: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	h.app.Store = state
	return h, state
}

// seriesFixture describes the GraphQL answers of one series.
type seriesFixture struct {
	id       string
	title    string
	seasons  []map[string]any
	episodes map[string][]map[string]any
}

// handler answers MovieDetails and EpisodesInSeasonData for the fixture.
func (f seriesFixture) handler() func(string, map[string]any) (string, int) {
	return func(operation string, variables map[string]any) (string, int) {
		switch operation {
		case "MovieDetails":
			if variables["movieId"] != f.id {
				return graphQLErrorResponse("unexpected movieId")
			}
			return dataResponse(map[string]any{
				"movie":  nil,
				"series": seriesData(f.title, f.seasons...),
			})
		case "EpisodesInSeasonData":
			seasonID, _ := variables["seasonId"].(string)
			episodes, ok := f.episodes[seasonID]
			if !ok {
				return graphQLErrorResponse("unknown season " + seasonID)
			}
			return dataResponse(episodesData(episodes...))
		default:
			return graphQLErrorResponse("unexpected operation " + operation)
		}
	}
}

func libraryEntry(t *testing.T, state *store.Store, url string) store.Series {
	t.Helper()
	entry, err := state.SeriesByURL(url)
	if err != nil {
		t.Fatalf("reading library entry: %v", err)
	}
	if entry == nil {
		t.Fatalf("library entry %q missing", url)
	}
	return *entry
}

func episodeIDs(refs []store.EpisodeRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

func TestIndexEpisodesSeries(t *testing.T) {
	h, _ := newLibraryHarness(t)
	fixture := seriesFixture{
		id:    "s1",
		title: "Test Show",
		seasons: []map[string]any{
			seasonData("season-1", 1, "Staffel 1"),
			seasonData("season-2", 2, "Staffel 2"),
		},
		episodes: map[string][]map[string]any{
			"season-1": {
				episodeData("e1", 1, "Folge Eins", ""),
				episodeData("e2", 2, "Folge Zwei", ""),
			},
			"season-2": {
				episodeData("e3", 1, "Folge Drei", ""),
			},
		},
	}
	h.backend.setGraphQL(fixture.handler())

	title, kind, refs, err := h.app.IndexEpisodes(context.Background(), "https://www.ardplus.de/details/s1-show")
	if err != nil {
		t.Fatalf("IndexEpisodes: %v", err)
	}
	if title != "Test Show" {
		t.Errorf("title = %q, want Test Show", title)
	}
	if kind != store.KindSeries {
		t.Errorf("kind = %q, want %q", kind, store.KindSeries)
	}
	want := []store.EpisodeRef{
		{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
		{ID: "e2", Title: "Folge Zwei", Season: 1, Number: 2},
		{ID: "e3", Title: "Folge Drei", Season: 2, Number: 1},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("refs = %#v, want %#v", refs, want)
	}
}

func TestIndexEpisodesMovie(t *testing.T) {
	h, _ := newLibraryHarness(t)
	h.backend.setGraphQL(func(operation string, variables map[string]any) (string, int) {
		if operation != "MovieDetails" {
			return graphQLErrorResponse("unexpected operation " + operation)
		}
		if variables["movieId"] != "m1" {
			return graphQLErrorResponse("unexpected movieId")
		}
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", "https://cdn.test/m1.mpd"),
			"series": nil,
		})
	})

	title, kind, refs, err := h.app.IndexEpisodes(context.Background(), "https://www.ardplus.de/details/m1-film")
	if err != nil {
		t.Fatalf("IndexEpisodes: %v", err)
	}
	if title != "Test Film" {
		t.Errorf("title = %q, want Test Film", title)
	}
	if kind != store.KindMovie {
		t.Errorf("kind = %q, want %q", kind, store.KindMovie)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %#v, want no episodes for a movie", refs)
	}
}

func TestIndexEpisodesTatort(t *testing.T) {
	h, _ := newLibraryHarness(t)
	h.backend.setCategory(func(slug string) (string, int) {
		if slug != "tatort-muenchen" {
			return "", 404
		}
		page := `<html><head><script type="application/ld+json">` +
			`{"itemListElement":[` +
			`{"url":"https://www.ardplus.de/details/t1-erste-folge","name":"Erste Folge"},` +
			`{"url":"https://www.ardplus.de/details/t2-zweite-folge","name":"Zweite Folge"}` +
			`]}</script></head></html>`
		return page, 200
	})

	title, kind, refs, err := h.app.IndexEpisodes(context.Background(), "https://www.ardplus.de/kategorie/tatort-muenchen")
	if err != nil {
		t.Fatalf("IndexEpisodes: %v", err)
	}
	// The title is not derived from the slug: the hub supplies it when the
	// entry is created, and CheckEntry falls back only when it is still empty.
	if title != "" {
		t.Errorf("title = %q, want empty", title)
	}
	if kind != store.KindTatort {
		t.Errorf("kind = %q, want %q", kind, store.KindTatort)
	}
	want := []string{"t1", "t2"}
	if got := episodeIDs(refs); !reflect.DeepEqual(got, want) {
		t.Errorf("episode ids = %v, want %v", got, want)
	}
	if refs[0].Title != "Erste Folge" || refs[1].Title != "Zweite Folge" {
		t.Errorf("episode titles = %q, %q", refs[0].Title, refs[1].Title)
	}
}

func TestIndexEpisodesInvalidURL(t *testing.T) {
	h, _ := newLibraryHarness(t)
	_, _, _, err := h.app.IndexEpisodes(context.Background(), "https://example.com/details/x")
	if err == nil {
		t.Fatal("IndexEpisodes accepted an unsupported host")
	}
	if !strings.Contains(err.Error(), "unsupported host: example.com") {
		t.Errorf("error = %v, want the host reason", err)
	}
}

func TestCheckEntryTracksNewEpisodesAndErrors(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	if _, err := state.UpsertSeries(url, "Old Title", store.KindMovie); err != nil {
		t.Fatal(err)
	}
	entry := libraryEntry(t, state, url)

	fixture := seriesFixture{
		id:      "s1",
		title:   "Test Show",
		seasons: []map[string]any{seasonData("season-1", 1, "Staffel 1")},
		episodes: map[string][]map[string]any{"season-1": {
			episodeData("e1", 1, "Folge Eins", ""),
			episodeData("e2", 2, "Folge Zwei", ""),
			episodeData("e3", 3, "Folge Drei", ""),
		}},
	}
	h.backend.setGraphQL(fixture.handler())

	first := h.app.CheckEntry(context.Background(), entry)
	if first.Err != nil {
		t.Fatalf("first check: %v", first.Err)
	}
	if first.Title != "Test Show" || first.Kind != store.KindSeries {
		t.Errorf("first check title/kind = %q/%q", first.Title, first.Kind)
	}
	if first.Total != 3 {
		t.Errorf("first check total = %d, want 3", first.Total)
	}
	if got, want := episodeIDs(first.New), []string{"e1", "e2", "e3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("first check new = %v, want %v", got, want)
	}

	stored := libraryEntry(t, state, url)
	if stored.Title != "Test Show" || stored.Kind != store.KindSeries {
		t.Errorf("stored title/kind = %q/%q, want Test Show/%s", stored.Title, stored.Kind, store.KindSeries)
	}
	if stored.CheckedAt == nil {
		t.Error("stored checked_at is nil after a check")
	}
	if stored.NewCount != 3 {
		t.Errorf("stored new_count = %d, want 3", stored.NewCount)
	}
	if stored.LastError != "" {
		t.Errorf("stored last_error = %q, want empty", stored.LastError)
	}

	second := h.app.CheckEntry(context.Background(), entry)
	if second.Err != nil {
		t.Fatalf("second check: %v", second.Err)
	}
	if len(second.New) != 0 {
		t.Errorf("second check new = %v, want none", episodeIDs(second.New))
	}
	if second.Total != 3 {
		t.Errorf("second check total = %d, want 3", second.Total)
	}

	// The API picks up one more episode: exactly that one is new.
	h.backend.setGraphQL(seriesFixture{
		id:      "s1",
		title:   "Test Show",
		seasons: []map[string]any{seasonData("season-1", 1, "Staffel 1")},
		episodes: map[string][]map[string]any{"season-1": {
			episodeData("e1", 1, "Folge Eins", ""),
			episodeData("e2", 2, "Folge Zwei", ""),
			episodeData("e3", 3, "Folge Drei", ""),
			episodeData("e4", 4, "Folge Vier", ""),
		}},
	}.handler())

	third := h.app.CheckEntry(context.Background(), entry)
	if third.Err != nil {
		t.Fatalf("third check: %v", third.Err)
	}
	if got, want := episodeIDs(third.New), []string{"e4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("third check new = %v, want %v", got, want)
	}

	// A failing API records the error on the library entry.
	h.backend.setGraphQL(func(operation string, _ map[string]any) (string, int) {
		return graphQLErrorResponse("boom " + operation)
	})
	failed := h.app.CheckEntry(context.Background(), entry)
	if failed.Err == nil {
		t.Fatal("CheckEntry did not report the API error")
	}
	if !strings.Contains(failed.Err.Error(), "boom") {
		t.Errorf("error = %v, want the API message", failed.Err)
	}
	stored = libraryEntry(t, state, url)
	if !strings.Contains(stored.LastError, "boom") {
		t.Errorf("stored last_error = %q, want the API error", stored.LastError)
	}
}

func TestCheckLibrarySkipsDisabledAndKeepsOrder(t *testing.T) {
	h, state := newLibraryHarness(t)
	fixtures := map[string]seriesFixture{
		"a1": {
			id:       "a1",
			title:    "Alpha Show",
			seasons:  []map[string]any{seasonData("a-season", 1, "Staffel 1")},
			episodes: map[string][]map[string]any{"a-season": {episodeData("a-e1", 1, "Alpha Eins", "")}},
		},
		"b2": {
			id:       "b2",
			title:    "Beta Show",
			seasons:  []map[string]any{seasonData("b-season", 1, "Staffel 1")},
			episodes: map[string][]map[string]any{"b-season": {episodeData("b-e1", 1, "Beta Eins", "")}},
		},
		"c3": {
			id:       "c3",
			title:    "Gamma Show",
			seasons:  []map[string]any{seasonData("c-season", 1, "Staffel 1")},
			episodes: map[string][]map[string]any{"c-season": {episodeData("c-e1", 1, "Gamma Eins", "")}},
		},
	}
	h.backend.setGraphQL(func(operation string, variables map[string]any) (string, int) {
		switch operation {
		case "MovieDetails":
			id, _ := variables["movieId"].(string)
			fixture := fixtures[id]
			return dataResponse(map[string]any{
				"movie":  nil,
				"series": seriesData(fixture.title, fixture.seasons...),
			})
		case "EpisodesInSeasonData":
			seasonID, _ := variables["seasonId"].(string)
			for _, fixture := range fixtures {
				if episodes, ok := fixture.episodes[seasonID]; ok {
					return dataResponse(episodesData(episodes...))
				}
			}
			return graphQLErrorResponse("unknown season " + seasonID)
		default:
			return graphQLErrorResponse("unexpected operation " + operation)
		}
	})

	urls := map[string]string{
		"a1": "https://www.ardplus.de/details/a1-alpha",
		"b2": "https://www.ardplus.de/details/b2-beta",
		"c3": "https://www.ardplus.de/details/c3-gamma",
	}
	for _, key := range []string{"a1", "b2", "c3"} {
		if _, err := state.UpsertSeries(urls[key], fixtures[key].title, store.KindSeries); err != nil {
			t.Fatal(err)
		}
	}
	beta := libraryEntry(t, state, urls["b2"])
	if err := state.SetSeriesEnabled(beta.ID, false); err != nil {
		t.Fatal(err)
	}

	results, err := h.app.CheckLibrary(context.Background())
	if err != nil {
		t.Fatalf("CheckLibrary: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("CheckLibrary returned %d results, want 2", len(results))
	}
	var gotURLs []string
	for _, result := range results {
		if result.Err != nil {
			t.Errorf("%s: %v", result.Series.URL, result.Err)
		}
		gotURLs = append(gotURLs, result.Series.URL)
	}
	wantURLs := []string{urls["a1"], urls["c3"]}
	if !reflect.DeepEqual(gotURLs, wantURLs) {
		t.Errorf("result order = %v, want %v", gotURLs, wantURLs)
	}

	// The library is ordered by title; the results mirror that order.
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatal(err)
	}
	var enabled []string
	for _, entry := range entries {
		if entry.Enabled {
			enabled = append(enabled, entry.URL)
		}
	}
	if !reflect.DeepEqual(enabled, gotURLs) {
		t.Errorf("enabled library order = %v, results = %v", enabled, gotURLs)
	}

	storedBeta := libraryEntry(t, state, urls["b2"])
	if storedBeta.CheckedAt != nil {
		t.Errorf("disabled entry was checked at %v", storedBeta.CheckedAt)
	}
}

func seedLibrary(t *testing.T, state *store.Store, url string, refs ...store.EpisodeRef) store.Series {
	t.Helper()
	id, err := state.UpsertSeries(url, "Test Show", store.KindSeries)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.SyncEpisodes(id, refs); err != nil {
		t.Fatal(err)
	}
	return libraryEntry(t, state, url)
}

func downloadFixture() seriesFixture {
	return seriesFixture{
		id:      "s1",
		title:   "Test Show",
		seasons: []map[string]any{seasonData("season-1", 1, "Staffel 1")},
		episodes: map[string][]map[string]any{"season-1": {
			episodeData("e1", 1, "Folge Eins", "https://cdn.test/e1.mpd"),
			episodeData("e2", 2, "Folge Zwei", "https://cdn.test/e2.mpd"),
			episodeData("e3", 3, "Folge Drei", "https://cdn.test/e3.mpd"),
		}},
	}
}

func TestDownloadNewDownloadsPendingEpisodesOnly(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	entry := seedLibrary(t, state, url,
		store.EpisodeRef{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
		store.EpisodeRef{ID: "e2", Title: "Folge Zwei", Season: 1, Number: 2},
		store.EpisodeRef{ID: "e3", Title: "Folge Drei", Season: 1, Number: 3},
	)
	h.backend.setGraphQL(downloadFixture().handler())

	if err := h.app.DownloadNew(context.Background(), entry); err != nil {
		t.Fatalf("DownloadNew: %v", err)
	}

	fetches := h.fetcher.fetchList()
	if len(fetches) != 3 {
		t.Fatalf("Fetch called %d times, want 3 (%v)", len(fetches), fetches)
	}
	wantManifests := []string{
		"https://cdn.test/e1.mpd?hdnts=fake-token",
		"https://cdn.test/e2.mpd?hdnts=fake-token",
		"https://cdn.test/e3.mpd?hdnts=fake-token",
	}
	if !reflect.DeepEqual(fetches, wantManifests) {
		t.Errorf("manifest URLs = %v, want %v", fetches, wantManifests)
	}

	// The queue labels identify every downloaded episode.
	wantLabels := []string{
		media.BuildEpisodePath("Test Show", 1, new(1), "Folge Eins"),
		media.BuildEpisodePath("Test Show", 1, new(2), "Folge Zwei"),
		media.BuildEpisodePath("Test Show", 1, new(3), "Folge Drei"),
	}
	var labels []string
	for _, event := range h.ui.eventsOf(EventQueued) {
		labels = append(labels, event.Label)
	}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("queued labels = %v, want %v", labels, wantLabels)
	}

	pending, err := state.Pending(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending after download = %v, want none", episodeIDs(knownRefs(pending)))
	}

	if err := h.app.DownloadNew(context.Background(), entry); err != nil {
		t.Fatalf("second DownloadNew: %v", err)
	}
	if got := h.fetcher.fetchList(); len(got) != 3 {
		t.Errorf("second DownloadNew fetched %d manifests, want no new work", len(got))
	}
	if got := h.fetcher.muxList(); len(got) != 3 {
		t.Errorf("second DownloadNew muxed %d files, want no new work", len(got))
	}
}

func knownRefs(episodes []store.KnownEpisode) []store.EpisodeRef {
	refs := make([]store.EpisodeRef, 0, len(episodes))
	for _, episode := range episodes {
		refs = append(refs, episode.EpisodeRef)
	}
	return refs
}

func TestDownloadNewMarksExistingFileDone(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	entry := seedLibrary(t, state, url,
		store.EpisodeRef{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
		store.EpisodeRef{ID: "e2", Title: "Folge Zwei", Season: 1, Number: 2},
	)
	h.backend.setGraphQL(downloadFixture().handler())

	seasonDir := filepath.Join(h.downloads, "Test.Show.S01.example")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(seasonDir, "Test.Show.S01E01."+harnessMediaTag+"-example.mp4")
	if err := os.WriteFile(existing, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.app.DownloadNew(context.Background(), entry); err != nil {
		t.Fatalf("DownloadNew: %v", err)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 1 || fetches[0] != "https://cdn.test/e2.mpd?hdnts=fake-token" {
		t.Errorf("fetched manifests = %v, want only e2", fetches)
	}
	pending, err := state.Pending(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want none: the existing episode must be marked downloaded", episodeIDs(knownRefs(pending)))
	}
}

func TestDownloadNewFailureKeepsEpisodePending(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	entry := seedLibrary(t, state, url,
		store.EpisodeRef{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
	)
	h.backend.setGraphQL(downloadFixture().handler())
	h.fetcher.fetchErr = errors.New("network down")

	err := h.app.DownloadNew(context.Background(), entry)
	if err == nil {
		t.Fatal("DownloadNew succeeded despite a failing download")
	}
	if !strings.Contains(err.Error(), "1 episode download(s) failed") {
		t.Errorf("error = %v, want the episode failure summary", err)
	}
	count, err := state.CountPending(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("pending count = %d, want the failed episode to stay pending", count)
	}
}

func TestDownloadEpisodesSkipsSeasonsWithoutRequestedEpisodes(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	entry := seedLibrary(t, state, url,
		store.EpisodeRef{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
		store.EpisodeRef{ID: "e2", Title: "Folge Zwei", Season: 1, Number: 2},
		store.EpisodeRef{ID: "e3", Title: "Folge Drei", Season: 2, Number: 1},
	)
	fixture := seriesFixture{
		id:    "s1",
		title: "Test Show",
		seasons: []map[string]any{
			seasonData("season-1", 1, "Staffel 1"),
			seasonData("season-2", 2, "Staffel 2"),
		},
		episodes: map[string][]map[string]any{
			"season-1": {
				episodeData("e1", 1, "Folge Eins", "https://cdn.test/e1.mpd"),
				episodeData("e2", 2, "Folge Zwei", "https://cdn.test/e2.mpd"),
			},
			"season-2": {
				episodeData("e3", 1, "Folge Drei", "https://cdn.test/e3.mpd"),
			},
		},
	}
	base := fixture.handler()
	seasonCalls := map[string]int{}
	h.backend.setGraphQL(func(operation string, variables map[string]any) (string, int) {
		if operation == "EpisodesInSeasonData" {
			seasonID, _ := variables["seasonId"].(string)
			seasonCalls[seasonID]++
		}
		return base(operation, variables)
	})

	if err := h.app.DownloadEpisodes(context.Background(), entry, []string{"e1"}); err != nil {
		t.Fatalf("DownloadEpisodes: %v", err)
	}
	if got := seasonCalls["season-2"]; got != 0 {
		t.Errorf("season 2 requested %d time(s), want none", got)
	}
	if got := seasonCalls["season-1"]; got != 1 {
		t.Errorf("season 1 requested %d time(s), want 1", got)
	}
	if h.app.episodeFailures != 0 {
		t.Errorf("episode failures = %d, want none", h.app.episodeFailures)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 1 || fetches[0] != "https://cdn.test/e1.mpd?hdnts=fake-token" {
		t.Errorf("fetched manifests = %v, want only e1", fetches)
	}
}

func TestDownloadEpisodesCountsFailuresForRequestedSeason(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	entry := seedLibrary(t, state, url,
		store.EpisodeRef{ID: "e1", Title: "Folge Eins", Season: 1, Number: 1},
		store.EpisodeRef{ID: "e2", Title: "Folge Zwei", Season: 1, Number: 2},
		store.EpisodeRef{ID: "e3", Title: "Folge Drei", Season: 2, Number: 1},
	)
	fixture := seriesFixture{
		id:    "s1",
		title: "Test Show",
		seasons: []map[string]any{
			seasonData("season-1", 1, "Staffel 1"),
			seasonData("season-2", 2, "Staffel 2"),
		},
		episodes: map[string][]map[string]any{
			"season-1": {
				episodeData("e1", 1, "Folge Eins", "https://cdn.test/e1.mpd"),
				episodeData("e2", 2, "Folge Zwei", "https://cdn.test/e2.mpd"),
			},
			"season-2": {
				episodeData("e3", 1, "Folge Drei", "https://cdn.test/e3.mpd"),
			},
		},
	}
	base := fixture.handler()
	seasonCalls := map[string]int{}
	h.backend.setGraphQL(func(operation string, variables map[string]any) (string, int) {
		if operation == "EpisodesInSeasonData" {
			seasonID, _ := variables["seasonId"].(string)
			seasonCalls[seasonID]++
			if seasonID == "season-1" {
				return graphQLErrorResponse("PersistedQueryNotFound")
			}
		}
		return base(operation, variables)
	})

	err := h.app.DownloadEpisodes(context.Background(), entry, []string{"e1", "e2"})
	if err == nil {
		t.Fatal("DownloadEpisodes succeeded despite a failing requested season")
	}
	if !strings.Contains(err.Error(), "2 episode download(s) failed") {
		t.Errorf("error = %v, want two episode failures", err)
	}
	if !strings.Contains(err.Error(), "could not fetch episodes for season 1") {
		t.Errorf("error = %v, want the season 1 fetch failure", err)
	}
	if h.app.episodeFailures != 2 {
		t.Errorf("episode failures = %d, want the two requested episodes", h.app.episodeFailures)
	}
	if got := seasonCalls["season-2"]; got != 0 {
		t.Errorf("season 2 requested %d time(s), want none", got)
	}
	if fetches := h.fetcher.fetchList(); len(fetches) != 0 {
		t.Errorf("fetched manifests = %v, want none", fetches)
	}
}

func TestRememberStoresURLAndMetadata(t *testing.T) {
	h, state := newLibraryHarness(t)
	const url = "https://www.ardplus.de/details/s1-show"
	if _, err := state.UpsertSeries(url, "Old Title", store.KindMovie); err != nil {
		t.Fatal(err)
	}

	h.app.Remember(url, "New Title", store.KindSeries)

	entry := libraryEntry(t, state, url)
	if entry.Title != "New Title" {
		t.Errorf("title = %q, want New Title", entry.Title)
	}
	if entry.Kind != store.KindSeries {
		t.Errorf("kind = %q, want %q", entry.Kind, store.KindSeries)
	}
	last, err := state.Setting(store.SettingLastURL)
	if err != nil {
		t.Fatal(err)
	}
	if last != url {
		t.Errorf("last_url = %q, want %q", last, url)
	}
	if entries, err := state.SeriesList(); err != nil || len(entries) != 1 {
		t.Errorf("library entries = %v (err %v), want the single remembered URL", entries, err)
	}
}
