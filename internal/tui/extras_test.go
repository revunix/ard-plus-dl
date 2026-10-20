package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestRevealPathWithoutAFile checks that opening a folder reports a problem
// instead of running a command.
func TestRevealPathWithoutAFile(t *testing.T) {
	if err := revealPath(""); err == nil {
		t.Fatal("an empty path must fail")
	}
	if err := revealPath(filepath.Join(t.TempDir(), "missing", "file.mp4")); err == nil {
		t.Fatal("a missing folder must fail")
	}
	// An existing folder is accepted, the command itself may be missing on a
	// machine without a desktop.
	dir := t.TempDir()
	if err := revealPath(filepath.Join(dir, "file.mp4")); err != nil {
		t.Logf("the file manager is not available: %v", err)
	}
}

// TestLibraryMarksEntries checks the marked entries of the library.
func TestLibraryMarksEntries(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if _, err := state.UpsertSeries("https://www.ardplus.de/details/"+title, title, store.KindSeries); err != nil {
			t.Fatalf("add series: %v", err)
		}
	}
	model := newModel(Options{Config: &config.Options{FileTag: "example"}, Store: state})
	model.screen = ScreenLibrary
	model.loadLibrary()
	if len(model.library) != 3 {
		t.Fatalf("library holds %d entries, want 3", len(model.library))
	}

	// m marks the entry under the cursor.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(*Model)
	if len(model.markedEntries()) != 1 {
		t.Fatalf("marked = %d entries, want 1", len(model.markedEntries()))
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(*Model)
	marked := model.markedEntries()
	if len(marked) != 2 {
		t.Fatalf("marked = %d entries, want 2", len(marked))
	}
	if marked[0].Title != "Alpha" || marked[1].Title != "Beta" {
		t.Fatalf("marked = %v, want the first two entries", marked)
	}
	if !strings.Contains(model.View(), "2 Einträge markiert") {
		t.Fatalf("the view misses the selection:\n%s", model.View())
	}

	// x asks for a confirmation first.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(*Model)
	if !strings.Contains(model.libMessage, "erneut drücken") {
		t.Fatalf("message = %q, want the confirmation", model.libMessage)
	}
	if len(model.library) != 3 {
		t.Fatalf("the entries were removed without a confirmation")
	}

	// The second x removes the marked entries.
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("removing the marked entries must return a command")
	}
	runCommands(t, model, cmd)
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("read library: %v", err)
	}
	if len(entries) != 1 || entries[0].Title != "Gamma" {
		t.Fatalf("library = %v, want only the unmarked entry", entries)
	}
}
