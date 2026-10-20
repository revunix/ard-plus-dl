package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestDownloadPathCheckKeepsFilesOutsideSafe checks that only files inside the
// downloads directory may be deleted.
func TestDownloadPathCheckKeepsFilesOutsideSafe(t *testing.T) {
	dir := t.TempDir()
	model := newModel(Options{Config: &config.Options{FileTag: "example", DownloadsDir: dir}})

	if err := model.checkDownloadPath(filepath.Join(dir, "Serie", "Folge.mp4")); err != nil {
		t.Fatalf("a file inside the downloads directory must be allowed: %v", err)
	}
	if err := model.checkDownloadPath("/etc/passwd"); err == nil {
		t.Fatal("a file outside the downloads directory must be refused")
	}
	if err := model.checkDownloadPath(filepath.Join(dir, "..", "escape.mp4")); err == nil {
		t.Fatal("a path that leaves the downloads directory must be refused")
	}
	if err := model.checkDownloadPath(""); err == nil {
		t.Fatal("an empty path must be refused")
	}
}

// TestRemovingAFileResetsTheEpisode checks that the file is deleted and the
// episode counts as open again.
func TestRemovingAFileResetsTheEpisode(t *testing.T) {
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/x", "Test Show", store.KindSeries); err != nil {
		t.Fatalf("add series: %v", err)
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("read library: %v", err)
	}
	seriesID := entries[0].ID
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{{ID: "e1", Title: "Erste", Season: 1, Number: 1}}); err != nil {
		t.Fatalf("sync episodes: %v", err)
	}

	// A file in the downloads directory.
	downloads := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("create downloads: %v", err)
	}
	file := filepath.Join(downloads, "Test Show S01E01 - Erste.mp4")
	if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := state.MarkDownloaded(seriesID, "e1", file); err != nil {
		t.Fatalf("mark downloaded: %v", err)
	}

	model := newModel(Options{Config: &config.Options{FileTag: "example", DownloadsDir: downloads}, Store: state})
	model.screen = ScreenQueue
	model.jobs = []job{{label: "Test Show S01E01 - Erste", path: file, status: "fertig", episodeID: "e1", seriesID: seriesID}}

	// The first x only asks, the second one deletes.
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(*Model)
	if cmd != nil {
		t.Fatal("the first x must only ask for a confirmation")
	}
	if !strings.Contains(model.libMessage, "erneut drücken") {
		t.Fatalf("message = %q, want the confirmation", model.libMessage)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the file was deleted without a confirmation: %v", err)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("the second x must delete the file")
	}
	runCommands(t, model, cmd)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file still exists: %v", err)
	}
	episodes, err := state.Episodes(seriesID)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(episodes))
	}
	if episodes[0].DownloadedAt != nil {
		t.Fatal("the episode still counts as downloaded")
	}
}

// TestResettingAnEntryClearsFilesAndHistory checks the reset of a series.
func TestResettingAnEntryClearsFilesAndHistory(t *testing.T) {
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/x", "Test Show", store.KindSeries); err != nil {
		t.Fatalf("add series: %v", err)
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("read library: %v", err)
	}
	seriesID := entries[0].ID
	if _, err := state.SyncEpisodes(seriesID, []store.EpisodeRef{{ID: "e1", Title: "Erste", Season: 1, Number: 1}}); err != nil {
		t.Fatalf("sync episodes: %v", err)
	}
	downloads := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("create downloads: %v", err)
	}
	file := filepath.Join(downloads, "Test Show S01E01 - Erste.mp4")
	if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := state.MarkDownloaded(seriesID, "e1", file); err != nil {
		t.Fatalf("mark downloaded: %v", err)
	}

	model := newModel(Options{Config: &config.Options{FileTag: "example", DownloadsDir: downloads}, Store: state})
	model.screen = ScreenLibrary
	model.library = entries

	// r asks first.
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(*Model)
	if cmd != nil {
		t.Fatal("the first r must only ask for a confirmation")
	}
	if !strings.Contains(model.libMessage, "erneut drücken") {
		t.Fatalf("message = %q, want the confirmation", model.libMessage)
	}

	// The second r resets the series.
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("the second r must reset the series")
	}
	runCommands(t, model, cmd)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file still exists: %v", err)
	}
	episodes, err := state.Episodes(seriesID)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("episodes = %d, want the history to be cleared", len(episodes))
	}
}
