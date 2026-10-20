package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// hubPage mimics the city subcategories in ARD Plus's SSR page data.
func hubPage(categories ...[2]string) string {
	var nodes []string
	for _, category := range categories {
		nodes = append(nodes, fmt.Sprintf(`{"title":%q,"slug":%q}`, category[0], category[1]))
	}
	return `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"category":{"slug":"tatort-cities","subcategories":{"nodes":[` +
		strings.Join(nodes, ",") + `]}}}}}</script>`
}

func categoryPage(ids ...string) string {
	var items []string
	for i, id := range ids {
		items = append(items, fmt.Sprintf(
			`{"@type":"ListItem","position":"%d","item":{"@type":"Movie","name":"Folge %s","url":"https://www.ardplus.de/details/%s-slug"}}`,
			i+1, id, id))
	}
	return `<!DOCTYPE html><html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"ItemList","itemListElement":[` +
		strings.Join(items, ",") + `]}</script></head><body></body></html>`
}

// TestHubFlowUsesSelectedCategories verifies that the hub resolves to a
// category choice and only the chosen categories are processed.
func TestHubFlowUsesSelectedCategories(t *testing.T) {
	h := newHarness(t, &config.Options{Skip: 1, FileTag: "example"})
	h.backend.setHub(func() (string, int) {
		return hubPage([2]string{"Tatort Koeln", "tatort-koeln"}, [2]string{"Tatort Hamburg", "tatort-hamburg"}), 200
	})
	h.backend.setCategory(func(slug string) (string, int) {
		switch slug {
		case "tatort-koeln":
			return categoryPage("koeln1", "koeln2"), 200
		case "tatort-hamburg":
			return categoryPage("hamburg1"), 200
		}
		return "", 404
	})
	h.backend.setGraphQL(movieDetailsHandler(map[string]map[string]string{
		"koeln1":   {"title": "Kölner Folge 1", "dash": "https://cdn.test/koeln1.mpd"},
		"koeln2":   {"title": "Kölner Folge 2", "dash": "https://cdn.test/koeln2.mpd"},
		"hamburg1": {"title": "Hamburger Folge", "dash": "https://cdn.test/hamburg.mpd"},
	}))
	h.ui.categories = []string{"tatort-koeln"}

	if err := h.app.runURL(context.Background(), "https://www.ardplus.de/tatort", 1); err != nil {
		t.Fatalf("runURL: %v", err)
	}

	if h.ui.categoryCalls != 1 {
		t.Fatalf("category prompts = %d, want 1", h.ui.categoryCalls)
	}
	labels := fmt.Sprint(h.ui.events)
	if !strings.Contains(labels, "Kölner Folge") {
		t.Fatalf("the chosen category was not processed: %s", labels)
	}
	if strings.Contains(labels, "Hamburger Folge") {
		t.Fatalf("an unchosen category was processed: %s", labels)
	}
}

// TestHubFlowAutomaticUsesAllCategories covers --automatic and the
// download-new path, which must not prompt.
func TestHubFlowAutomaticUsesAllCategories(t *testing.T) {
	h := newHarness(t, &config.Options{Automatic: true, Skip: 1, FileTag: "example"})
	h.backend.setHub(func() (string, int) {
		return hubPage([2]string{"Tatort Koeln", "tatort-koeln"}, [2]string{"Tatort Hamburg", "tatort-hamburg"}), 200
	})
	h.backend.setCategory(func(slug string) (string, int) {
		switch slug {
		case "tatort-koeln":
			return categoryPage("koeln1"), 200
		case "tatort-hamburg":
			return categoryPage("hamburg1"), 200
		}
		return "", 404
	})
	h.backend.setGraphQL(movieDetailsHandler(map[string]map[string]string{
		"koeln1":   {"title": "Kölner Folge", "dash": "https://cdn.test/k.mpd"},
		"hamburg1": {"title": "Hamburger Folge", "dash": "https://cdn.test/h.mpd"},
	}))

	if err := h.app.runURL(context.Background(), "https://www.ardplus.de/tatort", 1); err != nil {
		t.Fatalf("runURL: %v", err)
	}
	if h.ui.categoryCalls != 0 {
		t.Fatalf("automatic mode prompted %d time(s)", h.ui.categoryCalls)
	}
	labels := fmt.Sprint(h.ui.events)
	for _, want := range []string{"Kölner Folge", "Hamburger Folge"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("automatic mode skipped %q: %s", want, labels)
		}
	}
}

// The Tatort archive is replaced by one library entry per category, so the
// library shows categories instead of a single block of episodes.
func TestCheckEntryExpandsTatortHub(t *testing.T) {
	h, state := newLibraryHarness(t)
	h.backend.setHub(func() (string, int) {
		return hubPage([2]string{"Tatort Köln", "tatort-koeln"}, [2]string{"Tatort Münster", "tatort-muenster"}), 200
	})

	const hubURL = "https://www.ardplus.de/tatort"
	h.app.Remember(hubURL, "Tatort (alle)", store.KindTatort)
	entry := libraryEntry(t, state, hubURL)

	result := h.app.CheckEntry(context.Background(), entry)
	if result.Err != nil {
		t.Fatalf("CheckEntry: %v", result.Err)
	}
	if !strings.Contains(result.Note, "2 Kategorie-Einträge") {
		t.Fatalf("note = %q, want the expansion note", result.Note)
	}

	if gone, err := state.SeriesByURL(hubURL); err != nil || gone != nil {
		t.Fatalf("the archive entry should be gone: %+v (%v)", gone, err)
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("SeriesList: %v", err)
	}
	// The library stores the canonical ARD Plus URL, not the address of the
	// server the test happens to use.
	want := map[string]string{
		categoryURL("tatort-koeln"):    "Tatort Köln",
		categoryURL("tatort-muenster"): "Tatort Münster",
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v, want %d", entries, len(want))
	}
	for _, created := range entries {
		if want[created.URL] != created.Title || created.Kind != store.KindTatort {
			t.Fatalf("created entry = %+v", created)
		}
	}
}

// TestTatortShortURLCanonicalised covers /tatort-<stadt>.
func TestTatortShortURLCanonicalised(t *testing.T) {
	h := newHarness(t, &config.Options{Automatic: true, Skip: 1, FileTag: "example"})
	h.backend.setCategory(func(slug string) (string, int) {
		if slug != "tatort-koeln" {
			t.Errorf("slug = %q, want tatort-koeln", slug)
		}
		return categoryPage("koeln1"), 200
	})
	h.backend.setGraphQL(movieDetailsHandler(map[string]map[string]string{
		"koeln1": {"title": "Kölner Folge", "dash": "https://cdn.test/k.mpd"},
	}))
	if err := h.app.runURL(context.Background(), "https://www.ardplus.de/tatort-koeln", 1); err != nil {
		t.Fatalf("runURL: %v", err)
	}
	if !strings.Contains(fmt.Sprint(h.ui.events), "Kölner Folge") {
		t.Fatalf("episode was not processed: %v", h.ui.events)
	}
}

// movieDetailsHandler answers MovieDetails for a set of movie ids.
func movieDetailsHandler(movies map[string]map[string]string) func(string, map[string]any) (string, int) {
	return func(operation string, variables map[string]any) (string, int) {
		if operation != "MovieDetails" {
			return `{"data":{}}`, 200
		}
		id, _ := variables["movieId"].(string)
		movie, ok := movies[id]
		if !ok {
			return `{"errors":[{"message":"Movie not found"}]}`, 200
		}
		return fmt.Sprintf(`{"data":{"movie":{"id":%q,"title":%q,"productionYear":2024,"customData":{},"videoSource":{"dashUrl":%q}}}}`,
			id, movie["title"], movie["dash"]), 200
	}
}

// DownloadNew drives a fixed selection, so it must never prompt, even when the
// configuration is interactive (this used to panic on a nil UI).
func TestDownloadNewTatortDoesNotPrompt(t *testing.T) {
	h := newHarness(t, &config.Options{Skip: 1, FileTag: "example"})
	state, err := store.Open(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	h.app.Store = state

	const url = "https://www.ardplus.de/kategorie/tatort-koeln"
	h.backend.setCategory(func(string) (string, int) { return categoryPage("e1", "e2"), 200 })
	h.backend.setGraphQL(movieDetailsHandler(map[string]map[string]string{
		"e1": {"title": "Altes Eisen", "dash": "https://cdn.test/e1.mpd"},
		"e2": {"title": "Fiderallala", "dash": "https://cdn.test/e2.mpd"},
	}))

	id, err := state.UpsertSeries(url, "Tatort Koeln", store.KindTatort)
	if err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if _, err := state.SyncEpisodes(id, []store.EpisodeRef{
		{ID: "e1", Title: "Altes Eisen"},
		{ID: "e2", Title: "Fiderallala"},
	}); err != nil {
		t.Fatalf("SyncEpisodes: %v", err)
	}
	if err := state.MarkDownloaded(id, "e2"); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	entry := libraryEntry(t, state, url)

	if err := h.app.DownloadNew(context.Background(), entry); err != nil {
		t.Fatalf("DownloadNew: %v", err)
	}
	if h.ui.tatortCalls != 0 || h.ui.categoryCalls != 0 {
		t.Fatalf("DownloadNew prompted: tatort=%d categories=%d", h.ui.tatortCalls, h.ui.categoryCalls)
	}
	labels := fmt.Sprint(h.ui.events)
	if !strings.Contains(labels, "Altes Eisen") {
		t.Fatalf("the pending episode was not downloaded: %s", labels)
	}
	if strings.Contains(labels, "Fiderallala") {
		t.Fatalf("an already downloaded episode was fetched again: %s", labels)
	}
	pending, err := state.CountPending(id)
	if err != nil || pending != 0 {
		t.Fatalf("pending = %d (%v), want 0", pending, err)
	}
}

// A category entry added by URL has no title yet; the check derives one, while
// a title stored by the archive expansion is kept.
func TestCheckEntryKeepsStoredTatortTitle(t *testing.T) {
	h, state := newLibraryHarness(t)
	h.backend.setCategory(func(string) (string, int) { return categoryPage("k1"), 200 })

	const url = "https://www.ardplus.de/kategorie/tatort-muenster"
	h.app.Remember(url, "Tatort Münster", store.KindTatort)
	entry := libraryEntry(t, state, url)
	result := h.app.CheckEntry(context.Background(), entry)
	if result.Err != nil {
		t.Fatalf("CheckEntry: %v", result.Err)
	}
	if result.Title != "Tatort Münster" {
		t.Fatalf("title = %q, want the stored title", result.Title)
	}
	stored, err := state.SeriesByURL(url)
	if err != nil || stored == nil || stored.Title != "Tatort Münster" {
		t.Fatalf("stored entry = %+v (%v)", stored, err)
	}

	// A bare category URL gets a derived fallback title.
	const bare = "https://www.ardplus.de/kategorie/tatort-koeln"
	h.app.Remember(bare, "", store.KindTatort)
	entry = libraryEntry(t, state, bare)
	result = h.app.CheckEntry(context.Background(), entry)
	if result.Title != "Tatort Koeln" {
		t.Fatalf("fallback title = %q, want Tatort Koeln", result.Title)
	}
}
