package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func TestDetailsViewShowsEpisodeState(t *testing.T) {
	now := time.Now()
	model := newModel(Options{Config: &config.Options{}})
	model.details = store.Series{ID: 1, URL: "https://www.ardplus.de/details/x", Title: "Test Show", Kind: store.KindSeries}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Zweite Folge", Season: 1, Number: 2}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Erste Folge", Season: 1, Number: 1}, DownloadedAt: &now},
	}
	model.detailSeasonEpisodes = model.episodes
	model.activeDetailSeason = 1
	model.screen = ScreenSeasonEpisodes

	view := model.View()
	for _, want := range []string{"Test Show", "Staffel 1", "S01E02", "S01E01", "Zweite Folge", "offen", "geladen", "2 Folgen, davon 1 offen"} {
		if !strings.Contains(view, want) {
			t.Fatalf("details view misses %q:\n%s", want, view)
		}
	}
}

// Categories have no season/episode numbers, so the label column disappears.
func TestDetailsViewWithoutEpisodeNumbers(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	model.details = store.Series{ID: 1, Title: "Ballauf und Schenk", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Die Schöpfung"}},
	}
	model.screen = ScreenDetails
	view := model.View()
	if !strings.Contains(view, "Die Schöpfung") {
		t.Fatalf("episode title missing:\n%s", view)
	}
	if strings.Contains(view, "  -   ") || strings.Contains(view, " [ ] - ") {
		t.Fatalf("a numbering column is rendered without numbers:\n%s", view)
	}
}

func TestDetailsSelectionAndDownload(t *testing.T) {
	var gotIDs []string
	model := newModel(Options{
		Config: &config.Options{},
		DownloadEpisodes: func(_ context.Context, _ store.Series, ids []string) error {
			gotIDs = ids
			return nil
		},
	})
	model.details = store.Series{ID: 1, URL: "https://www.ardplus.de/details/x", Title: "Test Show", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste"}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite"}},
	}
	model.screen = ScreenDetails

	// Space selects one episode; the download uses exactly that selection.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	cmd := model.downloadDetails()
	if cmd == nil {
		t.Fatal("no download command")
	}
	cmd()
	model.busy = false
	if len(gotIDs) != 1 || gotIDs[0] != "a" {
		t.Fatalf("selected ids = %v, want [a]", gotIDs)
	}

	// Without a selection every open episode is downloaded.
	model.detailChoice = map[int]bool{}
	model.detailChoiceExplicit = false
	cmd = model.downloadDetails()
	if cmd == nil {
		t.Fatal("no download command for the open episodes")
	}
	cmd()
	if len(gotIDs) != 2 {
		t.Fatalf("open ids = %v, want both episodes", gotIDs)
	}
}

func TestDetailsEscapeReturnsToLibrary(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	model.screen = ScreenDetails
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(*Model).screen != ScreenLibrary {
		t.Fatalf("screen = %v, want library", updated.(*Model).screen)
	}
}

func TestOpeningSavedSeriesRefreshesAndShowsSeasonEpisodes(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	url := "https://www.ardplus.de/details/show"
	seriesID, err := state.UpsertSeries(url, "Test Show", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	refreshed := false
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		RefreshEntry: func(_ context.Context, entry store.Series) error {
			refreshed = true
			if entry.ID != seriesID {
				t.Fatalf("refreshed series ID = %d, want %d", entry.ID, seriesID)
			}
			_, err := state.SyncEpisodes(entry.ID, []store.EpisodeRef{
				{ID: "s1e1", Title: "Erste Folge", Season: 1, Number: 1},
				{ID: "s2e1", Title: "Zweite Staffel", Season: 2, Number: 1},
			})
			return err
		},
	})
	if model.screen != ScreenLibrary {
		t.Fatalf("initial screen = %v, want library", model.screen)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.screen != ScreenDetails || cmd == nil {
		t.Fatalf("opening series: screen = %v, refresh command = %v", model.screen, cmd != nil)
	}
	loadingView := model.View()
	if !strings.Contains(loadingView, "Folgenliste wird aktualisiert") || strings.Contains(loadingView, "Keine Folgen gespeichert") {
		t.Fatalf("loading view should show refresh state without claiming the list is empty:\n%s", loadingView)
	}
	model = runCommands(t, model, cmd)
	if !refreshed {
		t.Fatal("opening a saved series did not refresh its episode index")
	}
	view := model.View()
	for _, want := range []string{"Staffel 1 - 1 Folge, 1 offen", "Staffel 2 - 1 Folge, 1 offen"} {
		if !strings.Contains(view, want) {
			t.Errorf("season overview misses %q:\n%s", want, view)
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.screen != ScreenSeasonEpisodes {
		t.Fatalf("screen = %v, want season episodes", model.screen)
	}
	view = model.View()
	for _, want := range []string{"Staffel 1", "S01E01", "Erste Folge"} {
		if !strings.Contains(view, want) {
			t.Errorf("season episode view misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Zweite Staffel") {
		t.Errorf("season episode view included another season:\n%s", view)
	}
}

func TestOpeningSavedSeriesKeepsCachedEpisodesIfRefreshFails(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	url := "https://www.ardplus.de/details/show"
	seriesID, err := state.UpsertSeries(url, "Test Show", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{
		{ID: "saved-e1", Title: "Gespeicherte Folge", Season: 3, Number: 4},
	}); err != nil {
		t.Fatalf("save episode: %v", err)
	}
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		RefreshEntry: func(context.Context, store.Series) error {
			return errors.New("offline")
		},
	})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening a saved series did not start refresh")
	}
	model = runCommands(t, model, cmd)
	view := model.View()
	for _, want := range []string{"Staffel 3", "1 offen", "Folgenliste konnte nicht aktualisiert werden: offline"} {
		if !strings.Contains(view, want) {
			t.Errorf("season overview misses %q after refresh failure:\n%s", want, view)
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if view = model.View(); !strings.Contains(view, "S03E04") || !strings.Contains(view, "Gespeicherte Folge") {
		t.Fatalf("cached episode disappeared from season view after refresh failure:\n%s", view)
	}
}

func TestSeriesSeasonAndEpisodeDownloads(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	seriesID, err := state.UpsertSeries("https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{
		{ID: "s1e1", Title: "Staffel 1 Folge 1", Season: 1, Number: 1},
		{ID: "s1e2", Title: "Staffel 1 Folge 2", Season: 1, Number: 2},
		{ID: "s2e1", Title: "Staffel 2 Folge 1", Season: 2, Number: 1},
		{ID: "s2e2", Title: "Staffel 2 Folge 2", Season: 2, Number: 2},
	}); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	var downloads [][]string
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		DownloadEpisodes: func(_ context.Context, _ store.Series, ids []string) error {
			downloads = append(downloads, append([]string(nil), ids...))
			return nil
		},
	})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	model = runCommands(t, model, cmd)
	view := model.View()
	for _, want := range []string{"Staffel 1 - 2 Folgen, 2 offen", "Staffel 2 - 2 Folgen, 2 offen"} {
		if !strings.Contains(view, want) {
			t.Errorf("season overview misses %q:\n%s", want, view)
		}
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("season download did not start")
	}
	if model.screen != ScreenQueue {
		t.Fatalf("screen = %v, want the download pane", model.screen)
	}
	model = runCommands(t, model, cmd)
	if model.screen != ScreenDetails {
		t.Fatalf("screen = %v, want the season overview after the download", model.screen)
	}
	if len(downloads) != 1 || len(downloads[0]) != 2 || downloads[0][0] != "s1e2" || downloads[0][1] != "s1e1" {
		t.Fatalf("season download IDs = %v, want both Staffel 1 episodes", downloads)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.screen != ScreenSeasonEpisodes {
		t.Fatalf("screen = %v, want season episodes", model.screen)
	}
	view = model.View()
	for _, want := range []string{"Staffel 2", "S02E02", "S02E01"} {
		if !strings.Contains(view, want) {
			t.Errorf("season episode view misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Staffel 1 Folge") {
		t.Fatalf("season episode view included episodes from another season:\n%s", view)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeySpace},
		{Type: tea.KeyDown},
		{Type: tea.KeySpace},
	} {
		updated, _ = model.Update(key)
		model = updated.(*Model)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("selected episode download did not start")
	}
	if model.screen != ScreenQueue {
		t.Fatalf("screen = %v, want the download pane", model.screen)
	}
	model = runCommands(t, model, cmd)
	if model.screen != ScreenSeasonEpisodes || model.activeDetailSeason != 2 {
		t.Fatalf("returned to screen %v season %d, want Staffel 2", model.screen, model.activeDetailSeason)
	}
	if len(downloads) != 2 || len(downloads[1]) != 2 || downloads[1][0] != "s2e2" || downloads[1][1] != "s2e1" {
		t.Fatalf("selected episode IDs = %v, want both selected Staffel 2 episodes", downloads)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("single selected episode download did not start")
	}
	// The commands are executed, but the model state is not needed here.
	runCommands(t, model, cmd)
	if len(downloads) != 3 || len(downloads[2]) != 1 || downloads[2][0] != "s2e2" {
		t.Fatalf("single episode IDs = %v, want [s2e2]", downloads)
	}
}

func TestSeriesSeasonOverviewScrollsToLaterSeasons(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	model.details = store.Series{ID: 1, Title: "Long Show", Kind: store.KindSeries}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "s1", Season: 1, Number: 1}},
		{EpisodeRef: store.EpisodeRef{ID: "s2", Season: 2, Number: 1}},
		{EpisodeRef: store.EpisodeRef{ID: "s3", Season: 3, Number: 1}},
	}
	model.rebuildDetailSeasons()
	model.height = 13
	model.screen = ScreenDetails

	view := model.View()
	if !strings.Contains(view, "Staffel 1") || !strings.Contains(view, "Staffel 2") {
		t.Fatalf("initial short-screen season window is wrong:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	view = model.View()
	if !strings.Contains(view, "Staffel 3") {
		t.Fatalf("later season is unreachable by scrolling:\n%s", view)
	}
}

func TestLibraryDownloadShowsQueueAndReturnsToLibrary(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	seriesID, err := state.UpsertSeries("https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{{ID: "e1", Title: "Erste Folge", Season: 1, Number: 1}}); err != nil {
		t.Fatalf("save episode: %v", err)
	}
	called := false
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		DownloadNew: func(context.Context, store.Series) error {
			called = true
			return nil
		},
	})
	if model.screen != ScreenLibrary {
		t.Fatalf("initial screen = %v, want library", model.screen)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("library download did not start")
	}
	model = runCommands(t, model, cmd)
	if !called {
		t.Fatal("library download did not run")
	}
	if model.screen != ScreenLibrary {
		t.Fatalf("screen = %v, want the library after the download", model.screen)
	}
	if !strings.Contains(model.toast, "Folgen geladen") {
		t.Fatalf("library message = %q, want a completion message", model.toast)
	}
}

func TestEpisodeSelectionNeedsExplicitChoiceToDownload(t *testing.T) {
	downloads := 0
	model := newModel(Options{
		Config: &config.Options{},
		DownloadEpisodes: func(context.Context, store.Series, []string) error {
			downloads++
			return nil
		},
	})
	model.details = store.Series{ID: 1, Title: "Test Show", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste"}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite"}},
	}
	model.screen = ScreenDetails

	// Space selects and deselects the same episode.
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeySpace})
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if !model.detailChoiceExplicit {
		t.Fatal("toggling an episode must mark the selection explicitly")
	}
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil || downloads != 0 {
		t.Fatalf("command = %v, downloads = %d; want no download after deselecting", cmd != nil, downloads)
	}
	if model.detailMessage != "Keine Folge ausgewählt." {
		t.Errorf("detailMessage = %q, want the empty selection hint", model.detailMessage)
	}
}

func TestSelectAllTwiceDownloadsNothing(t *testing.T) {
	downloads := 0
	model := newModel(Options{
		Config: &config.Options{},
		DownloadEpisodes: func(context.Context, store.Series, []string) error {
			downloads++
			return nil
		},
	})
	model.details = store.Series{ID: 1, Title: "Test Show", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste"}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite"}},
	}
	model.screen = ScreenDetails

	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil || downloads != 0 {
		t.Fatalf("command = %v, downloads = %d; want no download after select-all twice", cmd != nil, downloads)
	}
	if model.detailMessage != "Keine Folge ausgewählt." {
		t.Errorf("detailMessage = %q, want the empty selection hint", model.detailMessage)
	}
}

func TestSelectAllAfterPartialDeselection(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	model.details = store.Series{ID: 1, Title: "Test Show", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste"}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite"}},
	}
	model.screen = ScreenDetails

	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeySpace})
	if model.detailChoice[0] {
		t.Fatal("space must deselect the first episode")
	}
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := model.selectedEpisodes(); len(got) != 2 {
		t.Fatalf("selectedEpisodes() = %v, want both episodes after select-all", got)
	}
}

func TestEpisodeSelectionFallsBackToOpenEpisodes(t *testing.T) {
	now := time.Now()
	model := newModel(Options{Config: &config.Options{}})
	model.details = store.Series{ID: 1, Title: "Test Show", Kind: store.KindTatort}
	model.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "open", Title: "Offen"}},
		{EpisodeRef: store.EpisodeRef{ID: "done", Title: "Geladen"}, DownloadedAt: &now},
	}
	model.screen = ScreenDetails

	if model.detailChoiceExplicit {
		t.Fatal("opening a list must not mark a choice explicitly")
	}
	if got := model.selectedEpisodes(); len(got) != 1 || got[0] != "open" {
		t.Fatalf("selectedEpisodes() = %v, want only the open episode", got)
	}
}

func TestLibraryRemovalOpensConfirmation(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	id, err := state.UpsertSeries("https://www.ardplus.de/details/show", "Test Show", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	model := newModel(Options{Config: &config.Options{}, Store: state})
	if model.screen != ScreenLibrary {
		t.Fatalf("initial screen = %v, want the library", model.screen)
	}

	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatal("x must only open the confirmation window")
	}
	if model.confirm == nil || model.confirm.kind != confirmRemoveEntry || model.confirm.entry.ID != id {
		t.Fatalf("confirm = %#v, want the removal of the entry", model.confirm)
	}
	if !strings.Contains(model.View(), "Eintrag entfernen") {
		t.Fatalf("the view misses the confirmation window:\n%s", model.View())
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("x removed the entry; library has %d entries", len(entries))
	}

	model, cmd = update(t, model, tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatal("esc must not remove anything")
	}
	if model.confirm != nil {
		t.Fatal("esc did not close the confirmation window")
	}

	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model, cmd = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter in the confirmation window must return the removal command")
	}
	model = runCommands(t, model, cmd)
	entries, err = state.SeriesList()
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the confirmed removal left %d entries", len(entries))
	}
	if model.confirm != nil {
		t.Errorf("the window is still open after the removal")
	}
}

func TestProgramRunsSeasonEpisodeDownloadFlow(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	seriesID, err := state.UpsertSeries("https://www.ardplus.de/details/show", "Program Test", store.KindSeries)
	if err != nil {
		t.Fatalf("save series: %v", err)
	}
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{{ID: "e1", Title: "Erste Folge", Season: 1, Number: 1}}); err != nil {
		t.Fatalf("save episode: %v", err)
	}
	var downloaded []string
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		DownloadEpisodes: func(_ context.Context, _ store.Series, ids []string) error {
			downloaded = append([]string(nil), ids...)
			return nil
		},
	})

	// Open the saved series and its episode list through the same messages the
	// program loop would deliver.
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(*Model)
	model = runCommands(t, model, cmd)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(*Model)
	if model.screen != ScreenSeasonEpisodes {
		t.Fatalf("screen = %v, want the season episode list:\n%s", model.screen, model.View())
	}

	// Select the single open episode and download it.
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
	} {
		next, cmd = model.Update(key)
		model = next.(*Model)
		if cmd != nil {
			model = runCommands(t, model, cmd)
		}
	}
	if len(downloaded) != 1 || downloaded[0] != "e1" {
		t.Fatalf("downloaded episodes = %v, final screen %v:\n%s", downloaded, model.screen, model.View())
	}
}
