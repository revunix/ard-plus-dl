package tui

import (
	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestShots writes the screens of the interface as ANSI files. The script
// docs/screenshots/render.py turns them into the images of the README.
func TestShots(t *testing.T) {
	if os.Getenv("DUMP_SHOTS") == "" {
		t.Skip("set DUMP_SHOTS=1")
	}
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	// A fixed time keeps the images the same between two runs.
	clock := headerClock
	headerClock = func() string { return "20:15" }
	defer func() { headerClock = clock }()

	dir := os.Getenv("DUMP_SHOTS")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}

	write := func(name string, model *Model) {
		model.width = 112
		model.height = 34
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(model.View()), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// The library with a few entries.
	library := shotModel()
	library.screen = ScreenLibrary
	library.library = shotLibrary()
	library.libCursor = 0
	write("library", library)

	// The library in the light scheme.
	applyTheme(ThemeLight)
	light := shotModel()
	light.screen = ScreenLibrary
	light.library = library.library
	light.libCursor = 0
	write("library-light", light)
	applyTheme(ThemeAuto)

	// The downloads with a running file and the activity log.
	queue := shotModel()
	queue.screen = ScreenQueue
	queue.jobs = []job{
		{label: "Tatort Köln - Der stille Gast - Die schwarze Spinne", path: "/Users/ard/Downloads/ARD Plus/Tatort Köln/Der stille Gast/Die schwarze Spinne.mp4", done: 412, total: 640, status: "lädt", startedAt: time.Now().Add(-3 * time.Minute), updatedAt: time.Now(), lastDone: 400, perSegment: 240 * time.Millisecond},
		{label: "Tatort Köln - Der stille Gast - Der stille Gast", path: "/Users/ard/Downloads/ARD Plus/Tatort Köln/Der stille Gast/Der stille Gast.mp4", done: 640, total: 640, status: "fertig"},
		{label: "Tatort Münster - Die schwarze Spinne", path: "/Users/ard/Downloads/ARD Plus/Tatort Münster/Die schwarze Spinne.mp4", done: 0, total: 512, status: "wartet"},
	}
	queue.log = []string{
		"Prüfe 3 Serien nach neuen Folgen.",
		"Tatort Köln hat 92 Episoden.",
		"Überspringe 0 Episode(n) in der ersten Staffel.",
		"Lade Tatort Köln - Der stille Gast - Die schwarze Spinne…",
		"Medien-Anfragen nutzen den Modus: manifest-cookies",
		"Segment 412 von 640 geladen.",
	}
	write("downloads", queue)

	// The episodes of a season.
	episodes := shotModel()
	episodes.screen = ScreenSeasonEpisodes
	episodes.details = store.Series{ID: 1, Title: "Tatort: Der stille Gast", Kind: store.KindTatort, URL: "https://www.ardplus.de/details/tatort-koeln"}
	episodes.activeDetailSeason = 12
	episodes.detailSeasonEpisodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "e1", Title: "Der stille Gast", Season: 12, Number: 1}},
		{EpisodeRef: store.EpisodeRef{ID: "e2", Title: "Die schwarze Spinne", Season: 12, Number: 2}},
		{EpisodeRef: store.EpisodeRef{ID: "e3", Title: "Das goldene Band", Season: 12, Number: 3}, DownloadedAt: timePtr(time.Now())},
		{EpisodeRef: store.EpisodeRef{ID: "e4", Title: "Niemand lebt für immer", Season: 12, Number: 4}},
	}
	episodes.detailCursor = 1
	episodes.detailChoice = map[int]bool{1: true}
	write("episodes", episodes)

	// The confirmation window before an entry leaves the library.
	confirm := shotModel()
	confirm.screen = ScreenLibrary
	confirm.library = library.library
	confirm.libCursor = 0
	confirm.askRemoveEntry(confirm.library[0])
	write("library-confirm", confirm)

	// The settings of a series.
	settings := shotModel()
	settings.screen = ScreenEditEntry
	settings.editEntry = library.library[0]
	settings.editSkip.SetValue("3")
	write("settings", settings)

	// The catalog with the title under the cursor.
	catalog := shotModel()
	catalog.screen = ScreenCatalog
	catalog.library = library.library
	catalog.catalog = shotCatalog()
	catalog.catalogGenres = topGenres(catalog.catalog, catalogGenreLimit)
	catalog.catalogTab = catalogTabMovies
	catalog.catalogCursor = 2
	write("catalog", catalog)

	// The help window over the library.
	help := shotModel()
	help.screen = ScreenLibrary
	help.library = library.library
	help.helpVisible = true
	write("help", help)

	// The splash screen.
	splash := shotModel()
	splash.screen = ScreenSplash
	splash.splashTarget = ScreenLibrary
	splash.version = "1.4.0"
	splash.busy = true
	write("splash", splash)
}

func timePtr(value time.Time) *time.Time {
	return &value
}

// shotModel builds a model with the given size.
func shotModel() *Model {
	model := newModel(Options{Config: &config.Options{DownloadsDir: "/Users/ard/Downloads/ARD Plus"}, Version: "1.4.0"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 112, Height: 34})
	shot := updated.(*Model)
	shot.library = shotLibrary()
	// A few messages of the session for the protocol box.
	day := time.Date(2026, 10, 28, 20, 11, 0, 0, time.Local)
	shot.history = []historyEntry{
		{at: day, text: "Bibliothek geladen (4 Einträge)"},
		{at: day.Add(14 * time.Second), text: "Tatort: Der stille Gast hat 2 neue Folgen"},
		{at: day.Add(15 * time.Second), text: "Tatort: Die schwarze Spinne hat 1 neue Folge"},
		{at: day.Add(16 * time.Second), text: "Bibliothek aktualisiert."},
	}
	return shot
}

var _ = app.Event{}

// shotLibrary returns the entries of the library in the images.
func shotLibrary() []store.Series {
	return []store.Series{
		{ID: 1, Title: "Tatort: Der stille Gast", Kind: store.KindTatort, URL: "https://www.ardplus.de/details/tatort-koeln", Enabled: true, NewCount: 2, AddedAt: time.Date(2026, 1, 8, 20, 11, 0, 0, time.Local), CheckedAt: timePtr(time.Date(2026, 10, 18, 21, 4, 0, 0, time.Local))},
		{ID: 2, Title: "Tatort: Die schwarze Spinne", Kind: store.KindTatort, URL: "https://www.ardplus.de/details/tatort-muenster", Enabled: true, NewCount: 1, AddedAt: time.Date(2026, 2, 14, 19, 2, 0, 0, time.Local), CheckedAt: timePtr(time.Date(2026, 10, 18, 21, 4, 0, 0, time.Local))},
		{ID: 3, Title: "Die Bülent Ceylan Show", Kind: store.KindSeries, URL: "https://www.ardplus.de/details/celebrity", Enabled: true, AddedAt: time.Date(2026, 3, 2, 8, 44, 0, 0, time.Local), CheckedAt: timePtr(time.Date(2026, 10, 17, 9, 12, 0, 0, time.Local))},
		{ID: 4, Title: "Tatort: Das goldene Band", Kind: store.KindTatort, URL: "https://www.ardplus.de/details/tatort-wien", Enabled: false, AddedAt: time.Date(2026, 3, 19, 12, 30, 0, 0, time.Local)},
	}
}

// shotCatalog returns the titles of the catalog in the images.
func shotCatalog() []ardplus.CatalogItem {
	movie := func(id, title string, year, minutes int, genres ...string) ardplus.CatalogItem {
		return ardplus.CatalogItem{ID: id, Title: title, Kind: ardplus.CatalogMovie, Year: year, Runtime: minutes * 60, Genres: genres}
	}
	series := func(id, title string, year, seasons int, genres ...string) ardplus.CatalogItem {
		return ardplus.CatalogItem{ID: id, Title: title, Kind: ardplus.CatalogSeries, Year: year, Seasons: seasons, Genres: genres}
	}
	items := []ardplus.CatalogItem{
		series("a0T01", "Abenteuer Nordsee", 2007, 1, "Doku", "Serien Dokus"),
		series("a0T02", "Aenne Burda - Die Wirtschaftswunderfrau", 2018, 1, "Drama", "Nach wahren Begebenheiten"),
		movie("a0S01", "Alles Isy", 2018, 89, "Drama", "Family"),
		movie("a0S02", "Atari: Game Over", 2014, 63, "Doku", "Dokumentationen"),
		movie("a0S03", "Berlin 1945", 2020, 179, "Doku", "Dokumentationen", "Highlights"),
		series("a0T03", "Candice Renoir", 2013, 8, "Krimi", "Internationale Serien"),
		series("a0T04", "Die Bülent Ceylan Show", 2021, 2, "Comedy"),
		series("a0T05", "In aller Freundschaft - Die jungen Ärzte", 2015, 9, "Drama", "Arztserien"),
		series("a0T06", "Manhunt", 2019, 1, "Krimi", "Thriller"),
		series("a0T07", "Sherlock", 2010, 4, "Krimi", "Internationale Serien", "Highlights"),
		movie("a0S04", "Tatort: Der stille Gast", 2019, 88, "Krimi", "Tatort", "Tatort Köln"),
		movie("a0S05", "Tatort: Die schwarze Spinne", 2020, 87, "Krimi", "Tatort", "Tatort Münster"),
	}
	items[4].Synopsis = "„Berlin 1945“ erzählt die Geschichte von Befreiern und Befreiten, von Vernichtung und Erlösung: ein Jahr der Stunde Null an dem Ort, der sich von einem Kriegsschauplatz in ein Trümmerfeld verwandelt, um kurz darauf wieder den Beginn einer neuen Zeit einzuleiten."
	return items
}
