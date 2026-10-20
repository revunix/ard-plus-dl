package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func featureStore(t *testing.T) *store.Store {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { state.Close() })
	return state
}

// TestSeriesSettingsAreEditable checks the settings of a single series.
func TestSeriesSettingsAreEditable(t *testing.T) {
	state := featureStore(t)
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/x", "Test Show", store.KindSeries); err != nil {
		t.Fatalf("add series: %v", err)
	}
	model := newModel(Options{Config: &config.Options{FileTag: "example"}, Store: state})
	model.screen = ScreenLibrary
	model.loadLibrary()
	if len(model.library) != 1 {
		t.Fatalf("library holds %d entries, want 1", len(model.library))
	}

	// e opens the settings with the values of the entry.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model = updated.(*Model)
	if model.screen != ScreenEditEntry {
		t.Fatalf("screen = %v, want the settings of the series", model.screen)
	}
	if !strings.Contains(model.View(), "Datei-Tag") || !strings.Contains(model.View(), "Überspringen") {
		t.Fatalf("the settings miss their fields:\n%s", model.View())
	}

	// The values are stored.
	model.editTag.SetValue("eigener")
	model.editSkip.SetValue("4")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("saving the settings must return a command")
	}
	runCommands(t, model, cmd)

	entry, err := state.SeriesByURL("https://www.ardplus.de/details/x")
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if entry.Tag != "eigener" {
		t.Fatalf("tag = %q, want %q", entry.Tag, "eigener")
	}
	if entry.Skip != 4 {
		t.Fatalf("skip = %d, want 4", entry.Skip)
	}
	if model.screen != ScreenLibrary {
		t.Fatalf("screen = %v, want the library after saving", model.screen)
	}
}

// TestSeriesSettingsRejectAnInvalidTag checks the validation.
func TestSeriesSettingsRejectAnInvalidTag(t *testing.T) {
	model := populatedScreen("library", 80, 24)
	model.screen = ScreenEditEntry
	model.editTag.SetValue("mit leerzeichen")
	model.editSkip.SetValue("0")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if cmd != nil {
		t.Fatal("an invalid tag must not be saved")
	}
	if model.editErr == "" {
		t.Fatal("an invalid tag must show an error")
	}
	if !strings.Contains(model.View(), "Datei-Tag") {
		t.Fatalf("the settings disappeared:\n%s", model.View())
	}
}

// TestSeriesSettingsEscapeDoesNotStore checks that escape leaves the values
// untouched.
func TestSeriesSettingsEscapeDoesNotStore(t *testing.T) {
	state := featureStore(t)
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/x", "Test Show", store.KindSeries); err != nil {
		t.Fatalf("add series: %v", err)
	}
	model := newModel(Options{Config: &config.Options{FileTag: "example"}, Store: state})
	model.screen = ScreenLibrary
	model.loadLibrary()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model = updated.(*Model)
	model.editTag.SetValue("eigener")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(*Model)
	if model.screen != ScreenLibrary {
		t.Fatalf("screen = %v, want the library", model.screen)
	}
	entry, err := state.SeriesByURL("https://www.ardplus.de/details/x")
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if entry.Tag != "" {
		t.Fatalf("tag = %q, want it unchanged", entry.Tag)
	}
}

// TestLibraryDetailShowsTheSettings checks the values in the detail column.
func TestLibraryDetailShowsTheSettings(t *testing.T) {
	model := populatedScreen("library", 110, 30)
	model.libCursor = 0
	model.library[0].Tag = "eigener"
	model.library[0].Skip = 3
	view := model.View()
	if !strings.Contains(view, "eigener") {
		t.Errorf("the details miss the file tag:\n%s", view)
	}
	if !strings.Contains(view, "3 Folgen") {
		t.Errorf("the details miss the skip count:\n%s", view)
	}
}

// TestCopyActionReportsTheResult checks that copying leaves a message.
func TestCopyActionReportsTheResult(t *testing.T) {
	model := populatedScreen("library", 80, 24)
	model.library[0].URL = "https://www.ardplus.de/details/test"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(*Model)
	if model.toast == "" {
		t.Fatal("copying must report the result")
	}
	if err := copyToClipboard(""); err == nil {
		t.Fatal("copying nothing must fail")
	}
}

// TestEstimateRemaining checks the estimate of the remaining time.
func TestEstimateRemaining(t *testing.T) {
	if _, ok := estimateRemaining(0, 10, time.Second); ok {
		t.Error("without progress there is no estimate")
	}
	if _, ok := estimateRemaining(10, 10, time.Second); ok {
		t.Error("a finished download has no remaining time")
	}
	if _, ok := estimateRemaining(1, 10, 0); ok {
		t.Error("without a duration there is no estimate")
	}
	remaining, ok := estimateRemaining(5, 10, 10*time.Second)
	if !ok {
		t.Fatal("a running download has an estimate")
	}
	if remaining != 10*time.Second {
		t.Errorf("remaining = %s, want 10s", remaining)
	}
	if label := remainingLabel(remaining); label != "noch ~10 s" {
		t.Errorf("label = %q, want %q", label, "noch ~10 s")
	}
	if label := remainingLabel(5 * time.Minute); label != "noch ~5 min" {
		t.Errorf("label = %q, want %q", label, "noch ~5 min")
	}
}

// TestJobMeasuresTheSpeed checks that the speed comes from the last update.
func TestJobMeasuresTheSpeed(t *testing.T) {
	model := newTestModel(80, 24)
	model.applyEvent(app.Event{Kind: app.EventQueued, Label: "Folge A"})
	model.applyEvent(app.Event{Kind: app.EventProgress, Label: "Folge A", Done: 2, Total: 10})
	// The first update has no predecessor, so there is no speed yet.
	if model.jobs[0].perSegment != 0 {
		t.Errorf("per segment = %s, want no value yet", model.jobs[0].perSegment)
	}
	time.Sleep(20 * time.Millisecond)
	model.applyEvent(app.Event{Kind: app.EventProgress, Label: "Folge A", Done: 4, Total: 10})
	if model.jobs[0].perSegment <= 0 {
		t.Fatalf("per segment = %s, want a measured value", model.jobs[0].perSegment)
	}
	remaining := model.jobRemaining(model.jobs[0])
	if remaining == "" {
		t.Error("the queue should show the remaining time")
	}
}

// TestLibrarySearchFiltersTheList checks the search field of the library.
func TestLibrarySearchFiltersTheList(t *testing.T) {
	model := populatedScreen("library", 80, 24)
	model.library = []store.Series{
		{ID: 1, Title: "Tatort Köln", Kind: store.KindTatort},
		{ID: 2, Title: "Tatort Münster", Kind: store.KindTatort},
		{ID: 3, Title: "Die Bülent Ceylan Show", Kind: store.KindSeries},
	}

	// The slash opens the search field.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(*Model)
	if !model.searching {
		t.Fatal("the slash must open the search field")
	}
	if !strings.Contains(model.View(), "Suche") {
		t.Fatalf("the view misses the search field:\n%s", model.View())
	}

	// The term filters the list.
	model.search.SetValue("münster")
	visible := model.visibleLibrary()
	if len(visible) != 1 || visible[0].Title != "Tatort Münster" {
		t.Fatalf("visible = %v, want the matching entry", visible)
	}
	model.clampLibOffset()
	if model.libCursor > len(visible)-1 {
		t.Fatalf("cursor = %d, want it inside the filtered list", model.libCursor)
	}

	// Escape clears the filter again.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(*Model)
	if model.searching {
		t.Fatal("escape must close the search field")
	}
	if model.searchTerm() != "" {
		t.Fatalf("search term = %q, want it empty", model.searchTerm())
	}
	if len(model.visibleLibrary()) != 3 {
		t.Fatalf("visible = %d entries, want all of them", len(model.visibleLibrary()))
	}
}

// TestLibrarySearchKeepsTheCursorValid checks the cursor after filtering.
func TestLibrarySearchKeepsTheCursorValid(t *testing.T) {
	model := populatedScreen("library", 80, 24)
	model.library = []store.Series{
		{ID: 1, Title: "Alpha", Kind: store.KindSeries},
		{ID: 2, Title: "Beta", Kind: store.KindSeries},
		{ID: 3, Title: "Gamma", Kind: store.KindSeries},
	}
	model.libCursor = 2
	model.search.SetValue("alpha")
	model.clampLibOffset()
	if model.libCursor != 0 {
		t.Fatalf("cursor = %d, want 0 after the filter shrank the list", model.libCursor)
	}
	entry, ok := model.libraryEntry()
	if !ok || entry.Title != "Alpha" {
		t.Fatalf("entry = %v, want the filtered one", entry)
	}
}
