package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func confirmTestModel(t *testing.T, count int) (*Model, *store.Store) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	for index := 0; index < count; index++ {
		url := fmt.Sprintf("https://www.ardplus.de/details/show-%d", index)
		title := fmt.Sprintf("Show %d", index+1)
		if _, err := state.UpsertSeries(url, title, store.KindSeries); err != nil {
			t.Fatalf("save series: %v", err)
		}
	}
	model := newModel(Options{Config: &config.Options{}, Store: state})
	model.width, model.height = 100, 30
	model.library, err = state.SeriesList()
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	return model, state
}

func TestConfirmWindowTakesTheKeys(t *testing.T) {
	model, _ := confirmTestModel(t, 1)
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || model.confirm == nil {
		t.Fatal("x must open the confirmation window")
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyRunes, Runes: []rune{'m'}},
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune{'?'}},
	} {
		model, cmd = update(t, model, key)
		if cmd != nil {
			t.Errorf("%q ran a command while the window was open", key.String())
		}
		if model.confirm == nil {
			t.Fatalf("%q closed the window", key.String())
		}
		if model.searching || model.helpVisible {
			t.Fatalf("%q changed the page behind the window", key.String())
		}
	}

	model, cmd = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("n must only close the window")
	}
	if model.confirm != nil {
		t.Fatal("n did not close the window")
	}
}

func TestConfirmWindowKeepsQFromQuitting(t *testing.T) {
	model, _ := confirmTestModel(t, 1)
	model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Fatal("q in the confirmation window must not quit the app")
	}
	if model.confirm != nil {
		t.Fatal("q did not close the window")
	}
}

func TestConfirmWindowCountsTheMarkedEntries(t *testing.T) {
	model, _ := confirmTestModel(t, 3)
	model.libSelected = map[int64]bool{model.library[0].ID: true, model.library[1].ID: true}
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || model.confirm == nil {
		t.Fatal("x must open the confirmation window")
	}
	view := model.View()
	for _, want := range []string{"2 Einträge entfernen", "abbrechen", "Dateien bleiben"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the window misses %q:\n%s", want, view)
		}
	}
}

func TestConfirmWindowFitsAShortTerminal(t *testing.T) {
	model, _ := confirmTestModel(t, 12)
	model.width, model.height = 40, 12
	marked := make([]store.Series, 0, len(model.library))
	marked = append(marked, model.library...)
	model.askRemoveMarked(marked)

	lines := strings.Split(model.View(), "\n")
	if len(lines) > model.height {
		t.Fatalf("the view has %d rows, want at most %d", len(lines), model.height)
	}
	view := model.View()
	for _, want := range []string{"12 Einträge entfernen", "weitere", "abbrechen"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the window misses %q:\n%s", want, view)
		}
	}
}

func TestConfirmWindowResetWarnsAboutFiles(t *testing.T) {
	model, _ := confirmTestModel(t, 1)
	model, cmd := update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil || model.confirm == nil || model.confirm.kind != confirmResetEntry {
		t.Fatal("r must open the reset window")
	}
	view := model.View()
	for _, want := range []string{"Serie zurücksetzen", "gelöscht", "Verlauf", "löschen"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the window misses %q:\n%s", want, view)
		}
	}
}
