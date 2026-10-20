package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func newStoreCmdTestStore(t *testing.T) *store.Store {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func storeCmdTestEntry(t *testing.T, state *store.Store, url, title, kind string) store.Series {
	t.Helper()
	id, err := state.UpsertSeries(url, title, kind)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("series %d missing from the library", id)
	return store.Series{}
}

func TestStoreCmdLoadLibraryFillsModel(t *testing.T) {
	state := newStoreCmdTestStore(t)
	storeCmdTestEntry(t, state, "https://www.ardplus.de/details/alpha", "Alpha", store.KindSeries)
	storeCmdTestEntry(t, state, "https://www.ardplus.de/details/beta", "Beta", store.KindSeries)

	model := newModel(Options{Config: &config.Options{}, Store: state})
	model.library = nil
	cmd := model.loadLibraryCmd()
	if cmd == nil {
		t.Fatal("loadLibraryCmd returned no command")
	}
	model, _ = update(t, model, cmd())
	if len(model.library) != 2 {
		t.Fatalf("library = %#v, want two entries", model.library)
	}
	if model.library[0].Title != "Alpha" || model.library[1].Title != "Beta" {
		t.Fatalf("library order = %q, %q; want Alpha, Beta", model.library[0].Title, model.library[1].Title)
	}
}

func TestStoreCmdPendingCountStartsDownload(t *testing.T) {
	state := newStoreCmdTestStore(t)
	entry := storeCmdTestEntry(t, state, "https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	if _, err := state.SyncEpisodes(entry.ID, []store.EpisodeRef{
		{ID: "s1e1", Title: "Erste Folge", Season: 1, Number: 1},
	}); err != nil {
		t.Fatalf("save episode: %v", err)
	}
	var downloaded store.Series
	model := newModel(Options{
		Config: &config.Options{FileTag: "example"},
		Store:  state,
		DownloadNew: func(_ context.Context, chosen store.Series) error {
			downloaded = chosen
			return nil
		},
	})

	countCmd := model.pendingCountCmd(entry)
	if countCmd == nil {
		t.Fatal("pendingCountCmd returned no command")
	}
	msg, ok := countCmd().(pendingCountMsg)
	if !ok {
		t.Fatalf("pending count command returned %T", countCmd())
	}
	if msg.count != 1 {
		t.Fatalf("pending count = %d, want 1", msg.count)
	}
	model, downloadCmd := update(t, model, msg)
	if downloadCmd == nil {
		t.Fatal("one open episode must start a download")
	}
	if model.screen != ScreenQueue {
		t.Fatalf("screen = %v, want the download pane", model.screen)
	}
	result, ok := downloadCmd().(downloadNewDoneMsg)
	if !ok {
		t.Fatalf("download command returned %T", downloadCmd())
	}
	if result.entry.ID != entry.ID || downloaded.ID != entry.ID {
		t.Fatalf("downloaded entry = %d/%d, want %d", result.entry.ID, downloaded.ID, entry.ID)
	}
}

func TestStoreCmdPendingCountReportsNoOpenEpisodes(t *testing.T) {
	state := newStoreCmdTestStore(t)
	entry := storeCmdTestEntry(t, state, "https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	called := false
	model := newModel(Options{
		Config: &config.Options{FileTag: "example"},
		Store:  state,
		DownloadNew: func(context.Context, store.Series) error {
			called = true
			return nil
		},
	})

	countCmd := model.pendingCountCmd(entry)
	if countCmd == nil {
		t.Fatal("pendingCountCmd returned no command")
	}
	model, cmd := update(t, model, countCmd())
	if cmd != nil {
		t.Fatalf("no open episodes returned a command: %v", cmd)
	}
	if called {
		t.Fatal("no open episodes must not start a download")
	}
	if want := "Test Show: keine offenen Folgen."; model.toast != want {
		t.Fatalf("toast = %q, want %q", model.toast, want)
	}
}

func TestStoreCmdSetEnabledReloadsLibrary(t *testing.T) {
	state := newStoreCmdTestStore(t)
	entry := storeCmdTestEntry(t, state, "https://www.ardplus.de/details/alpha", "Alpha", store.KindSeries)
	model := newModel(Options{Config: &config.Options{}, Store: state})
	model.pendingRemoval = entry.ID

	cmd := model.setEnabledCmd(entry, false)
	if cmd == nil {
		t.Fatal("setEnabledCmd returned no command")
	}
	msg, ok := cmd().(storeActionMsg)
	if !ok {
		t.Fatalf("set enabled command returned %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("set enabled: %v", msg.err)
	}
	if want := "Alpha: aktualisiert."; msg.message != want {
		t.Fatalf("message = %q, want %q", msg.message, want)
	}
	model, reload := update(t, model, msg)
	if reload == nil {
		t.Fatal("a store action must reload the library")
	}
	if model.pendingRemoval != 0 {
		t.Fatalf("pendingRemoval = %d, want 0", model.pendingRemoval)
	}
	model, _ = update(t, model, reload())
	if len(model.library) != 1 || model.library[0].Enabled {
		t.Fatalf("library = %#v, want the disabled entry", model.library)
	}
}

func TestStoreCmdRemoveSeriesReloadsLibrary(t *testing.T) {
	state := newStoreCmdTestStore(t)
	entry := storeCmdTestEntry(t, state, "https://www.ardplus.de/details/alpha", "Alpha", store.KindSeries)
	model := newModel(Options{Config: &config.Options{}, Store: state})
	model.pendingRemoval = entry.ID

	cmd := model.removeSeriesCmd(entry)
	if cmd == nil {
		t.Fatal("removeSeriesCmd returned no command")
	}
	msg, ok := cmd().(storeActionMsg)
	if !ok {
		t.Fatalf("remove command returned %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("remove series: %v", msg.err)
	}
	if want := "Alpha entfernt."; msg.message != want {
		t.Fatalf("message = %q, want %q", msg.message, want)
	}
	model, reload := update(t, model, msg)
	if reload == nil {
		t.Fatal("a store action must reload the library")
	}
	model, _ = update(t, model, reload())
	if len(model.library) != 0 {
		t.Fatalf("library = %#v, want it empty", model.library)
	}
}

func TestStoreCmdOpenDetailsLoadsEpisodesAndSeason(t *testing.T) {
	state := newStoreCmdTestStore(t)
	entry := storeCmdTestEntry(t, state, "https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	if _, err := state.SyncEpisodes(entry.ID, []store.EpisodeRef{
		{ID: "s1e1", Title: "Erste Folge", Season: 1, Number: 1},
		{ID: "s2e1", Title: "Zweite Staffel", Season: 2, Number: 1},
	}); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	model := newModel(Options{Config: &config.Options{}, Store: state})

	cmd := model.openDetails(entry)
	if model.screen != ScreenDetails {
		t.Fatalf("screen = %v, want details", model.screen)
	}
	if !strings.Contains(model.detailMessage, "Folgenliste wird geladen") {
		t.Fatalf("detailMessage = %q, want the loading hint", model.detailMessage)
	}
	if cmd == nil {
		t.Fatal("openDetails returned no command")
	}
	model, _ = update(t, model, cmd())
	if len(model.episodes) != 2 {
		t.Fatalf("episodes = %#v, want two", model.episodes)
	}
	if model.detailMessage != "" {
		t.Fatalf("detailMessage = %q after loading, want empty", model.detailMessage)
	}
	if len(model.detailSeasons) != 2 {
		t.Fatalf("detailSeasons = %#v, want two seasons", model.detailSeasons)
	}
	if model.screen != ScreenDetails {
		t.Fatalf("screen = %v without a return season, want details", model.screen)
	}

	// A load triggered by a download returns to the season it came from.
	cmd = model.openDetails(entry)
	model.detailReturnSeason = 2
	model, _ = update(t, model, cmd())
	if model.screen != ScreenSeasonEpisodes {
		t.Fatalf("screen = %v, want the season episodes", model.screen)
	}
	if model.activeDetailSeason != 2 {
		t.Fatalf("activeDetailSeason = %d, want 2", model.activeDetailSeason)
	}
	if len(model.detailSeasonEpisodes) != 1 || model.detailSeasonEpisodes[0].ID != "s2e1" {
		t.Fatalf("season episodes = %#v, want only s2e1", model.detailSeasonEpisodes)
	}
	if model.detailReturnSeason != 0 {
		t.Fatalf("detailReturnSeason = %d, want it reset", model.detailReturnSeason)
	}
}
