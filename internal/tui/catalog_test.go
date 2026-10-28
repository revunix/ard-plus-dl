package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func testCatalog() []ardplus.CatalogItem {
	return []ardplus.CatalogItem{
		{ID: "a0S1", Title: "Atari: Game Over", Kind: ardplus.CatalogMovie, Year: 2014, Runtime: 3808, Synopsis: "Die Jagd ist eröffnet.", Genres: []string{"Doku"}},
		{ID: "a0T1", Title: "Die Bülent Ceylan Show", Kind: ardplus.CatalogSeries, Year: 2021, Seasons: 2, Genres: []string{"Comedy"}},
		{ID: "a0S2", Title: "Tatort: Der stille Gast", Kind: ardplus.CatalogMovie, Year: 2019, Runtime: 5300, Genres: []string{"Krimi", "Tatort", "Tatort: 2015 - 2020"}},
		{ID: "a0S3", Title: "Tatort: Die schwarze Spinne", Kind: ardplus.CatalogMovie, Year: 2020, Runtime: 5250, Genres: []string{"Krimi", "Tatort"}},
	}
}

// catalogModel returns a model with a library on disk and a catalog loader
// that counts its calls.
func catalogModel(t *testing.T, width, height int) (*Model, *int) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { state.Close() })
	loads := 0
	model := newModel(Options{
		Config: &config.Options{},
		Store:  state,
		Run:    func(context.Context) error { return nil },
		Catalog: func(context.Context) ([]ardplus.CatalogItem, error) {
			loads++
			return testCatalog(), nil
		},
		RefreshEntry: func(context.Context, store.Series) error { return nil },
	})
	model.screen = ScreenLibrary
	model, _ = update(t, model, tea.WindowSizeMsg{Width: width, Height: height})
	return model, &loads
}

// runCmd runs a command and feeds its message back into the model.
func runCmd(t *testing.T, model *Model, cmd tea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, inner := range batch {
			model = runCmd(t, model, inner)
		}
		return model
	}
	if msg == nil {
		return model
	}
	model, _ = update(t, model, msg)
	return model
}

func pressKey(t *testing.T, model *Model, key string) (*Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	return update(t, model, msg)
}

func TestCatalogOpensFromTheLibraryAndLoadsOnce(t *testing.T) {
	model, loads := catalogModel(t, 120, 34)
	model, cmd := pressKey(t, model, "tab")
	if model.screen != ScreenCatalog {
		t.Fatalf("screen = %v, want the catalog", model.screen)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Katalog") {
		t.Fatalf("catalog view misses its title while loading:\n%s", view)
	}
	model = runCmd(t, model, cmd)
	if *loads != 1 || len(model.catalog) != 4 {
		t.Fatalf("loads = %d, catalog = %d titles", *loads, len(model.catalog))
	}

	// The catalog opens on the series.
	view := ansi.Strip(model.View())
	for _, want := range []string{"1 Serien · 3 Filme", "Serien 1", "Filme 3", "Alle 4", "Suche", "Die Bülent Ceylan Show", "STAFFELN"} {
		if !strings.Contains(view, want) {
			t.Errorf("catalog view misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Atari: Game Over") {
		t.Errorf("the series tab lists a movie:\n%s", view)
	}
	model, _ = pressKey(t, model, "right")
	view = ansi.Strip(model.View())
	for _, want := range []string{"Atari: Game Over", "LÄNGE", "1:03 h", "2014", "Die Jagd ist eröffnet.", "1 h 03 min"} {
		if !strings.Contains(view, want) {
			t.Errorf("catalog view misses %q:\n%s", want, view)
		}
	}
	assertFitsWidth(t, model.View(), 120)
	assertFitsHeight(t, model.View(), 34)

	// Back to the library and into the catalog again: it stays loaded.
	model, _ = pressKey(t, model, "tab")
	if model.screen != ScreenLibrary {
		t.Fatalf("tab should lead back to the library, screen = %v", model.screen)
	}
	model, cmd = pressKey(t, model, "tab")
	model = runCmd(t, model, cmd)
	if *loads != 1 {
		t.Fatalf("the catalog was loaded %d times, want once", *loads)
	}
}

func TestCatalogFiltersBySearchAndGenre(t *testing.T) {
	model, _ := catalogModel(t, 120, 34)
	model, cmd := pressKey(t, model, "tab")
	model = runCmd(t, model, cmd)
	model, _ = pressKey(t, model, "3")

	// The genre filter starts with the most frequent genre.
	model, _ = pressKey(t, model, "g")
	if model.catalogGenre != "Krimi" {
		t.Fatalf("first genre = %q, want Krimi (genres %v)", model.catalogGenre, model.catalogGenres)
	}
	if got := len(model.visibleCatalog()); got != 2 {
		t.Fatalf("Krimi shows %d titles, want 2", got)
	}
	for _, genre := range model.catalogGenres {
		if strings.Contains(genre, ":") {
			t.Fatalf("the year buckets of the Tatort archive are no genre: %q", genre)
		}
	}
	model, _ = pressKey(t, model, "esc")
	if model.catalogGenre != "" || len(model.visibleCatalog()) != 4 {
		t.Fatal("esc should clear the filter")
	}

	model, _ = pressKey(t, model, "/")
	model = typeText(t, model, "spinne")
	if got := model.visibleCatalog(); len(got) != 1 || got[0].ID != "a0S3" {
		t.Fatalf("search shows %v", got)
	}
	model, _ = pressKey(t, model, "enter")
	if model.catalogSearching {
		t.Fatal("enter should end the search")
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Tatort: Die schwarze Spinne") || strings.Contains(view, "Atari") {
		t.Fatalf("filtered view:\n%s", view)
	}
}

func TestCatalogAddsTitlesToTheLibrary(t *testing.T) {
	model, _ := catalogModel(t, 120, 34)
	model, cmd := pressKey(t, model, "tab")
	model = runCmd(t, model, cmd)
	model, _ = pressKey(t, model, "3")

	// a adds the series under the cursor without leaving the catalog.
	model, _ = pressKey(t, model, "down")
	model, _ = pressKey(t, model, "a")
	if model.screen != ScreenCatalog {
		t.Fatalf("a should stay in the catalog, screen = %v", model.screen)
	}
	if len(model.library) != 1 {
		t.Fatalf("library has %d entries, want 1", len(model.library))
	}
	entry := model.library[0]
	if entry.URL != "https://www.ardplus.de/details/a0T1-die-buelent-ceylan-show" || entry.Kind != store.KindSeries || entry.Title != "Die Bülent Ceylan Show" {
		t.Fatalf("library entry = %+v", entry)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "in der Bibliothek") {
		t.Fatalf("catalog does not mark the title as saved:\n%s", view)
	}
	// A second a does not add the title twice.
	model, _ = pressKey(t, model, "a")
	if len(model.library) != 1 {
		t.Fatalf("library has %d entries after adding twice", len(model.library))
	}

	// enter opens a movie: it lands in the library and its details open;
	// esc leads back to the catalog.
	model, _ = pressKey(t, model, "home")
	model, cmd = pressKey(t, model, "enter")
	if model.screen != ScreenDetails || model.details.Title != "Atari: Game Over" || model.details.Kind != store.KindMovie {
		t.Fatalf("enter should open the movie, screen = %v details = %+v", model.screen, model.details)
	}
	model = runCmd(t, model, cmd)
	model, _ = pressKey(t, model, "esc")
	if model.screen != ScreenCatalog {
		t.Fatalf("esc should lead back to the catalog, screen = %v", model.screen)
	}

	// Titles that were added by hand are found by their content id.
	if _, ok := model.libraryEntryFor(ardplus.CatalogItem{ID: "a0S1"}); !ok {
		t.Fatal("the library entry of the movie is not found by its id")
	}
}

func TestCatalogTabsSplitSeriesAndMovies(t *testing.T) {
	model, _ := catalogModel(t, 120, 34)
	model, cmd := pressKey(t, model, "tab")
	model = runCmd(t, model, cmd)

	kinds := func() map[string]int {
		counts := map[string]int{}
		for _, item := range model.visibleCatalog() {
			counts[item.Kind]++
		}
		return counts
	}
	if got := kinds(); got[ardplus.CatalogSeries] != 1 || got[ardplus.CatalogMovie] != 0 {
		t.Fatalf("series tab = %v", got)
	}
	model, _ = pressKey(t, model, "2")
	if got := kinds(); got[ardplus.CatalogSeries] != 0 || got[ardplus.CatalogMovie] != 3 {
		t.Fatalf("movie tab = %v", got)
	}
	model, _ = pressKey(t, model, "right")
	if got := len(model.visibleCatalog()); got != 4 || model.catalogTab != catalogTabAll {
		t.Fatalf("tab with all titles shows %d", got)
	}
	// The arrows wrap around at both ends.
	model, _ = pressKey(t, model, "right")
	if model.catalogTab != catalogTabSeries {
		t.Fatalf("right after the last tab = %v, want the series", model.catalogTab)
	}
	model, _ = pressKey(t, model, "left")
	if model.catalogTab != catalogTabAll {
		t.Fatalf("left before the first tab = %v, want all titles", model.catalogTab)
	}

	// The tabs count the hits of the search; words match in any order.
	model, _ = pressKey(t, model, "/")
	model = typeText(t, model, "gast tatort")
	view := ansi.Strip(model.View())
	for _, want := range []string{"Serien 0", "Filme 1", "Alle 1", "Tatort: Der stille Gast"} {
		if !strings.Contains(view, want) {
			t.Errorf("search view misses %q:\n%s", want, view)
		}
	}
	// Switching the tab keeps the search.
	model, _ = pressKey(t, model, "enter")
	model, _ = pressKey(t, model, "1")
	if model.catalogSearch.Value() != "gast tatort" || len(model.visibleCatalog()) != 0 {
		t.Fatalf("series tab with the search shows %d titles", len(model.visibleCatalog()))
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Nichts gefunden.") {
		t.Fatalf("empty tab misses its hint:\n%s", view)
	}
}

func TestCatalogShowsLoadErrors(t *testing.T) {
	model, _ := catalogModel(t, 100, 30)
	model.opts.Catalog = func(context.Context) ([]ardplus.CatalogItem, error) {
		return nil, fmt.Errorf("HTTP 503")
	}
	model, cmd := pressKey(t, model, "tab")
	model = runCmd(t, model, cmd)
	view := ansi.Strip(model.View())
	for _, want := range []string{"Katalog nicht erreichbar.", "HTTP 503", "erneut laden"} {
		if !strings.Contains(view, want) {
			t.Errorf("error view misses %q:\n%s", want, view)
		}
	}
}

func TestCatalogFitsTheTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, 12}, {80, 24}, {100, 30}, {160, 40}} {
		model, _ := catalogModel(t, size.width, size.height)
		model, cmd := pressKey(t, model, "tab")
		model = runCmd(t, model, cmd)
		for index := 0; index < 3; index++ {
			model, _ = pressKey(t, model, "down")
		}
		view := model.View()
		assertFitsWidth(t, view, size.width)
		assertFitsHeight(t, view, size.height)
		if !strings.Contains(ansi.Strip(view), "▌") {
			t.Errorf("%dx%d: the cursor is not visible:\n%s", size.width, size.height, view)
		}
	}
}

func TestCatalogIsTheHomeScreen(t *testing.T) {
	loads := 0
	model := newModel(Options{
		Config: &config.Options{},
		Splash: true,
		Catalog: func(context.Context) ([]ardplus.CatalogItem, error) {
			loads++
			return testCatalog(), nil
		},
	})
	if model.splashTarget != ScreenCatalog {
		t.Fatalf("splash target = %v, want the catalog", model.splashTarget)
	}
	// The catalog loads while the splash screen shows.
	model = runCmd(t, model, model.loadCatalogCmd())
	if loads != 1 || len(model.catalog) != 4 {
		t.Fatalf("loads = %d, catalog = %d titles", loads, len(model.catalog))
	}
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 120, Height: 34})
	model, _ = update(t, model, splashDoneMsg{})
	if model.screen != ScreenCatalog {
		t.Fatalf("screen after the splash = %v, want the catalog", model.screen)
	}
	// esc on the home screen stays there.
	model, _ = pressKey(t, model, "esc")
	if model.screen != ScreenCatalog {
		t.Fatalf("esc left the home screen for %v", model.screen)
	}
	// s opens the settings, esc leads home again.
	model, _ = pressKey(t, model, "s")
	if model.screen != ScreenStart || model.formPurpose != formSettings {
		t.Fatalf("s should open the settings, screen = %v", model.screen)
	}
	model, _ = pressKey(t, model, "esc")
	if model.screen != ScreenCatalog {
		t.Fatalf("esc in the settings leads to %v, want the catalog", model.screen)
	}
}

func TestLoginFormAsksForNoURL(t *testing.T) {
	model := newModel(Options{
		Config:      &config.Options{},
		StartScreen: true,
		NeedsLogin:  true,
		Catalog:     func(context.Context) ([]ardplus.CatalogItem, error) { return testCatalog(), nil },
	})
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})
	if model.screen != ScreenStart || model.focus != 1 {
		t.Fatalf("screen = %v focus = %d, want the form on the user name", model.screen, model.focus)
	}
	view := ansi.Strip(model.View())
	if strings.Contains(view, "URL") {
		t.Fatalf("the form still asks for a URL:\n%s", view)
	}
	// The focus walks around without landing on the hidden field.
	for range 8 {
		model, _ = pressKey(t, model, "tab")
		if model.focus == 0 {
			t.Fatal("tab focused the hidden URL field")
		}
	}
	for range 8 {
		model, _ = update(t, model, tea.KeyMsg{Type: tea.KeyShiftTab})
		if model.focus == 0 {
			t.Fatal("shift+tab focused the hidden URL field")
		}
	}

	model.focusField(1)
	model = typeText(t, model, "user")
	model.focusField(2)
	model = typeText(t, model, "secret")
	model.focusField(3)
	model = typeText(t, model, "example")
	model, cmd := pressKey(t, model, "enter")
	if model.startErr != nil {
		t.Fatalf("form refused: %v", model.startErr)
	}
	if model.screen != ScreenCatalog || model.running {
		t.Fatalf("after the login screen = %v running = %v, want the catalog", model.screen, model.running)
	}
	_ = cmd
}

func TestLibraryLeadsToTheCatalog(t *testing.T) {
	model, _ := catalogModel(t, 120, 34)
	model.screen = ScreenLibrary
	model, cmd := pressKey(t, model, "a")
	if model.screen != ScreenCatalog {
		t.Fatalf("a in the library leads to %v, want the catalog", model.screen)
	}
	model = runCmd(t, model, cmd)
	model.screen = ScreenLibrary
	model, _ = pressKey(t, model, "esc")
	if model.screen != ScreenCatalog {
		t.Fatalf("esc in the library leads to %v, want the catalog", model.screen)
	}
}
