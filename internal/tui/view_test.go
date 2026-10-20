package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestViewsRender exercises View for every screen so a rendering regression or
// panic in the layout code fails a test.
func TestViewsRender(t *testing.T) {
	noop := func(context.Context) error { return nil }
	cfg := &config.Options{FileTag: "example"}

	t.Run("start", func(t *testing.T) {
		model := newModel(Options{Config: cfg, StartScreen: true, NeedsLogin: true, Run: noop})
		view := model.View()
		for _, want := range []string{"Start", "URL", "Benutzer", "Passwort", "Datei-Tag", "starten", "▸"} {
			if !strings.Contains(view, want) {
				t.Fatalf("start view misses %q:\n%s", want, view)
			}
		}
	})

	t.Run("series", func(t *testing.T) {
		model := newModel(Options{Config: cfg, Run: noop})
		reply := make(chan []int, 1)
		updated, _ := model.Update(SeasonsRequest{
			Show: "Test Show",
			Seasons: []app.Season{
				{Number: 1, Title: "Erste", ID: "s1"},
				{Number: 2, Title: "Zweite", ID: "s2"},
			},
			Reply: reply,
		})
		view := updated.(*Model).View()
		for _, want := range []string{"Test Show", "Staffel 1", "Staffel 2", "[x]", "▸", "wählen"} {
			if !strings.Contains(view, want) {
				t.Fatalf("series view misses %q:\n%s", want, view)
			}
		}
	})

	t.Run("queue progress", func(t *testing.T) {
		model := newModel(Options{Config: cfg, AutoStart: true, Run: noop})
		updated, _ := model.Update(EventMsg{Event: app.Event{Kind: app.EventQueued, Label: "Folge 1"}})
		model = updated.(*Model)
		updated, _ = model.Update(EventMsg{Event: app.Event{Kind: app.EventProgress, Label: "Folge 1", Done: 3, Total: 4}})
		model = updated.(*Model)
		view := model.View()
		for _, want := range []string{"Downloads", "Datei 1 von 1", "Folge 1", "lädt", progressLabel(3, 4), "abbrechen"} {
			if !strings.Contains(view, want) {
				t.Fatalf("queue view misses %q:\n%s", want, view)
			}
		}

		updated, _ = model.Update(EventMsg{Event: app.Event{Kind: app.EventDone, Label: "Folge 1", Path: "/tmp/out.mp4"}})
		if view := updated.(*Model).View(); !strings.Contains(view, "1 fertig") || !strings.Contains(view, "/tmp/out.mp4") {
			t.Fatalf("finished job or output path is not rendered:\n%s", view)
		}
	})

	t.Run("summary", func(t *testing.T) {
		model := newModel(Options{Config: cfg, Run: noop})
		updated, _ := model.Update(RunDoneMsg{Err: nil})
		view := updated.(*Model).View()
		if !strings.Contains(view, "Fertig") || !strings.Contains(view, "✓") || updated.(*Model).ExitCode() != 0 {
			t.Fatalf("summary view wrong:\n%s (exit %d)", view, updated.(*Model).ExitCode())
		}
	})

	t.Run("window size", func(t *testing.T) {
		model := newModel(Options{Config: cfg, StartScreen: true, Run: noop})
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		if got := updated.(*Model); got.width != 100 || got.height != 40 {
			t.Fatalf("window size not stored: %+v", got)
		}
	})
}

func TestDownloadQueueStacksFullWidth(t *testing.T) {
	longLabel := "Tatort Duisburg - Folge 252 - Der Fall Horst Schimanski mit einem sehr langen Dateinamen der bis zum Ende sichtbar bleiben soll"
	longPath := "/downloads/Tatort Duisburg/Der Fall Horst Schimanski/der-sehr-lange-dateiname-fuer-den-test.mp4"

	build := func(width int) *Model {
		model := newModel(Options{Config: &config.Options{FileTag: "example"}})
		model.screen = ScreenQueue
		model.width = width
		model.height = 30
		model.jobs = []job{
			{label: "Fertige Folge", status: "fertig"},
			{label: "Fehlerhafte Folge", status: "fehlgeschlagen"},
			{label: longLabel, path: longPath, done: 3, total: 4, status: "lädt"},
		}
		model.log = []string{
			"Lade " + longLabel + "…",
			"Medien-Anfragen nutzen den Modus: manifest-cookies",
		}
		return model
	}

	for _, width := range []int{120, 160} {
		t.Run(fmt.Sprintf("width/%d", width), func(t *testing.T) {
			model := build(width)
			view := model.View()
			expected := min(width, maxContentWidth)
			assertFitsWidth(t, view, expected)
			if hasCombinedLine(view, "Aktueller Download", "Aktivität") {
				t.Fatalf("download and activity panels share a row at width %d:\n%s", width, view)
			}
			for _, title := range []string{"Aktueller Download", "Aktivität"} {
				border := panelBorderLine(view, title)
				if border == "" {
					t.Fatalf("panel %q missing:\n%s", title, view)
				}
				if got := lipgloss.Width(border); got != expected {
					t.Fatalf("panel %q uses %d columns, want %d:\n%s", title, got, expected, view)
				}
			}

			plain := ansi.Strip(view)
			for _, want := range []string{"○ lädt", progressLabel(3, 4), "1 fertig · 1 fehlgeschlagen", "manifest-cookies"} {
				if !strings.Contains(plain, want) {
					t.Errorf("download view misses %q:\n%s", want, view)
				}
			}
			if !strings.Contains(plain, "█") && !strings.Contains(plain, "░") {
				t.Errorf("download view misses the progress bar:\n%s", view)
			}
			if !strings.Contains(plain, longLabel[:90]) {
				t.Errorf("file name is cut before 90 characters at width %d:\n%s", width, view)
			}
			if !strings.Contains(plain, longPath[:60]) {
				t.Errorf("target path is cut before 60 characters at width %d:\n%s", width, view)
			}
		})
	}

	for _, size := range []struct{ width, height int }{{40, 12}, {80, 24}, {120, 30}} {
		t.Run(fmt.Sprintf("bounds/%dx%d", size.width, size.height), func(t *testing.T) {
			model := build(size.width)
			model.height = size.height
			view := model.View()
			assertFitsWidth(t, view, min(size.width, maxContentWidth))
			assertFitsHeight(t, view, size.height)
		})
	}
}

// panelBorderLine returns the top border that carries the panel title.
func panelBorderLine(view, title string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, title) && strings.Contains(line, "╭") {
			return line
		}
	}
	return ""
}

// hasCombinedLine reports whether one rendered row carries both texts.
func hasCombinedLine(view, left, right string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, left) && strings.Contains(line, right) {
			return true
		}
	}
	return false
}

func TestResponsiveLayoutsStayWithinBounds(t *testing.T) {
	widths := []int{40, 79, 80, 99, 119, 120, 160}
	heights := []int{12, 16, 24}
	screens := []string{"start", "library", "series", "tatort", "details", "season episodes", "queue", "summary", "help"}
	for _, width := range widths {
		for _, height := range heights {
			for _, screen := range screens {
				t.Run(fmt.Sprintf("%s/%dx%d", screen, width, height), func(t *testing.T) {
					model := populatedScreen(screen, width, height)
					view := model.View()
					if strings.TrimSpace(view) == "" {
						t.Fatal("view is empty")
					}
					limit := width
					if limit > maxContentWidth {
						limit = maxContentWidth
					}
					assertFitsWidth(t, view, limit)
					assertFitsHeight(t, view, height)
				})
			}
		}
	}
}

func TestTuiScreensFitCommonSizes(t *testing.T) {
	sizes := []struct{ width, height int }{{40, 12}, {80, 24}, {120, 30}}
	screens := []string{"start", "library", "series", "tatort", "details", "season episodes", "queue", "summary", "help"}
	for _, size := range sizes {
		for _, screen := range screens {
			t.Run(fmt.Sprintf("%s/%dx%d", screen, size.width, size.height), func(t *testing.T) {
				view := populatedScreen(screen, size.width, size.height).View()
				if strings.TrimSpace(view) == "" {
					t.Fatal("view is empty")
				}
				assertFitsWidth(t, view, min(size.width, maxContentWidth))
				assertFitsHeight(t, view, size.height)
			})
		}
	}
}

// TestTuiScreensShowChromeAndSelection checks the redesigned chrome: header,
// footer and the cursor marker on every list screen.
func TestTuiScreensShowChromeAndSelection(t *testing.T) {
	cases := []struct {
		screen string
		header string
		footer string
		cursor bool
		extra  string
	}{
		{"start", "Start", "tab", true, "Zugang"},
		{"library", "Bibliothek", "öffnen", true, " neu"},
		{"series", "sehr langer Titel", "wählen", true, "[x]"},
		{"tatort", "Tatort", "starten", false, "0 bis 5"},
		{"details", "sehr langer Titel", "Hilfe", true, "Staffeln"},
		{"season episodes", "sehr langer Titel", "wählen", true, "○ offen"},
		{"queue", "Downloads", "abbrechen", false, "lädt"},
		{"summary", "Fertig", "Bibliothek", false, "✓"},
		{"help", "Hilfe", "schließen", false, "BIBLIOTHEK"},
	}
	for _, tc := range cases {
		t.Run(tc.screen, func(t *testing.T) {
			view := populatedScreen(tc.screen, 80, 24).View()
			for _, want := range []string{tc.header, tc.footer, tc.extra} {
				if !strings.Contains(view, want) {
					t.Errorf("%s view misses %q:\n%s", tc.screen, want, view)
				}
			}
			if got := strings.Contains(view, "▸"); got != tc.cursor {
				t.Errorf("%s view cursor marker = %v, want %v:\n%s", tc.screen, got, tc.cursor, view)
			}
			assertFitsWidth(t, view, 80)
			assertFitsHeight(t, view, 24)
		})
	}
}

func TestTuiEpisodeStatusPills(t *testing.T) {
	now := time.Now()
	model := newTestModel(80, 24)
	model.screen = ScreenSeasonEpisodes
	model.details = store.Series{Title: "Test Show", Kind: store.KindSeries}
	model.activeDetailSeason = 1
	model.detailSeasonEpisodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste Folge", Season: 1, Number: 1}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite Folge", Season: 1, Number: 2}, DownloadedAt: &now},
	}

	view := model.View()
	for _, want := range []string{"Test Show", "S01E01", "S01E02", "○ offen", "✓ geladen", "▸"} {
		if !strings.Contains(view, want) {
			t.Fatalf("episode view misses %q:\n%s", want, view)
		}
	}
}

func TestTuiLibraryBadgesRender(t *testing.T) {
	model := newTestModel(80, 24)
	model.screen = ScreenLibrary
	model.library = []store.Series{
		{ID: 1, Title: "Aktive Serie", Kind: store.KindSeries, Enabled: true, NewCount: 3},
		{ID: 2, Title: "Alte Serie", Kind: store.KindTatort, Enabled: false},
	}

	view := model.View()
	for _, want := range []string{"Aktive Serie", "Alte Serie", "Serie", "Tatort", "3 neu", "× "} {
		if !strings.Contains(view, want) {
			t.Fatalf("library view misses %q:\n%s", want, view)
		}
	}
}

func TestLibraryKeepsCursorVisible(t *testing.T) {
	model := newTestModel(80, 12)
	model.screen = ScreenLibrary
	for index := 0; index < 100; index++ {
		entry := store.Series{
			ID:       int64(index + 1),
			Title:    fmt.Sprintf("Serie %03d mit einem ziemlich langen Titel", index),
			Kind:     store.KindSeries,
			Enabled:  index%7 != 0,
			NewCount: index % 5,
		}
		if index%3 == 0 {
			checked := time.Date(2026, 1, 2, 15, 4, 0, 0, time.Local)
			entry.CheckedAt = &checked
		}
		model.library = append(model.library, entry)
	}
	model.libCursor = 60
	model.libOffset = 58

	view := model.View()
	if !strings.Contains(view, "Serie 060") {
		t.Fatalf("cursor entry is not visible:\n%s", view)
	}
	if !strings.Contains(view, "▸") {
		t.Fatalf("cursor marker is not visible:\n%s", view)
	}
	assertFitsWidth(t, view, 80)
	assertFitsHeight(t, view, 12)
}

func TestDetailURLIsTruncated(t *testing.T) {
	model := newTestModel(60, 24)
	model.screen = ScreenDetails
	model.details = store.Series{
		ID:    1,
		Title: "Ein sehr langer Serientitel, der nicht in eine schmale Zeile passt",
		URL:   "https://www.ardplus.de/details/" + strings.Repeat("sehr-langer-pfad/", 20),
		Kind:  store.KindMovie,
	}

	view := model.View()
	if !strings.Contains(view, "…") {
		t.Fatalf("details view does not truncate the URL:\n%s", view)
	}
	if strings.Contains(view, model.details.URL) {
		t.Fatalf("full URL should not be rendered:\n%s", view)
	}
	assertFitsWidth(t, view, 60)
}

func TestHelpView(t *testing.T) {
	model := newTestModel(120, 30)
	model.helpVisible = true
	view := model.View()
	for _, want := range []string{"Hilfe", "BIBLIOTHEK", "STAFFELN/FOLGEN", "DOWNLOAD", "START", "FENSTER", "schließen"} {
		if !strings.Contains(view, want) {
			t.Errorf("help view misses %q:\n%s", want, view)
		}
	}
	assertFitsWidth(t, view, 120)
	assertFitsHeight(t, view, 30)
	if !hasCombinedLine(view, "BIBLIOTHEK", "DOWNLOAD") {
		t.Errorf("help view at 120 should arrange groups in two columns:\n%s", view)
	}

	model = newTestModel(80, 24)
	model.helpVisible = true
	compact := model.View()
	for _, want := range []string{"Hilfe", "BIBLIOTHEK", "DOWNLOAD", "START"} {
		if !strings.Contains(compact, want) {
			t.Errorf("help view at 80x24 misses %q:\n%s", want, compact)
		}
	}
	assertFitsHeight(t, compact, 24)
	if hasCombinedLine(compact, "BIBLIOTHEK", "START") {
		t.Errorf("help view at 80 should stack groups in one column:\n%s", compact)
	}

	model = newTestModel(40, 12)
	model.helpVisible = true
	short := model.View()
	if !strings.Contains(short, "Hilfe") {
		t.Fatalf("short help view misses the header:\n%s", short)
	}
	assertFitsWidth(t, short, 40)
	assertFitsHeight(t, short, 12)

	model = newTestModel(30, 24)
	model.helpVisible = true
	if turned := model.View(); !strings.Contains(turned, "Hilfe") {
		t.Fatalf("help should win over the small-terminal hint:\n%s", turned)
	}
}

func TestLibrarySubtitleCounts(t *testing.T) {
	model := newTestModel(80, 24)
	model.screen = ScreenLibrary
	model.library = []store.Series{
		{ID: 1, Title: "Aktive Serie", Kind: store.KindSeries, Enabled: true, NewCount: 2},
		{ID: 2, Title: "Zweite Serie", Kind: store.KindSeries, Enabled: true, NewCount: 3},
		{ID: 3, Title: "Alte Serie", Kind: store.KindTatort, Enabled: false},
	}

	view := model.View()
	for _, want := range []string{"3 Einträge", "5 neu", "1 aus"} {
		if !strings.Contains(view, want) {
			t.Errorf("library subtitle misses %q:\n%s", want, view)
		}
	}
	assertFitsWidth(t, view, 80)
}

func TestDetailStatsRenderValueBeforeLabel(t *testing.T) {
	model := newTestModel(120, 30)
	model.screen = ScreenDetails
	model.details = store.Series{Title: "Serie", Kind: store.KindSeries}
	model.detailSeasons = []detailSeasonSummary{
		{number: 1, total: 2, open: 1},
		{number: 2, total: 1, open: 1},
	}

	view := ansi.Strip(model.View())
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "Staffeln") || !strings.Contains(line, "Folgen") {
			continue
		}
		if got := strings.Join(strings.Fields(line), " "); got != "2 Staffeln 3 Folgen 2 offen" {
			t.Fatalf("detail stats = %q, want value before label", got)
		}
		return
	}
	t.Fatalf("detail stats line missing:\n%s", view)
}

func TestPanelTitleSitsInTopBorder(t *testing.T) {
	lines := renderPanel(80, "Rahmen", []string{"Inhalt"}, true)
	if len(lines) != 3 {
		t.Fatalf("panel with one content line uses %d lines, want 3: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "Rahmen") {
		t.Fatalf("panel title is not in the top border: %q", lines[0])
	}
}

func TestTerminalTooSmallHint(t *testing.T) {
	narrow := newTestModel(39, 24)
	if view := narrow.View(); !strings.Contains(view, "Terminal zu klein (mindestens 40x12).") {
		t.Fatalf("narrow terminal hint missing:\n%s", view)
	}
	short := newTestModel(80, 11)
	if view := short.View(); !strings.Contains(view, "Terminal zu klein (mindestens 40x12).") {
		t.Fatalf("short terminal hint missing:\n%s", view)
	}
	minimum := newTestModel(40, 12)
	if view := minimum.View(); strings.Contains(view, "Terminal zu klein") {
		t.Fatalf("40x12 must render the screen:\n%s", view)
	}
}

func TestFitLineAndTruncateHandleANSIAndWideRunes(t *testing.T) {
	styled := titleStyle.Render("abcdefghij")
	cut := fitLine(styled, 6)
	if width := lipgloss.Width(cut); width > 6 {
		t.Fatalf("styled cut width = %d, want <= 6: %q", width, cut)
	}
	if !strings.Contains(cut, "…") {
		t.Fatalf("styled cut misses ellipsis: %q", cut)
	}

	for _, value := range []string{"你好世界你好", "🎬🎬🎬🎬"} {
		cut := fitLine(value, 5)
		if width := lipgloss.Width(cut); width > 5 {
			t.Fatalf("wide rune cut width = %d, want <= 5: %q", width, cut)
		}
		if !strings.Contains(cut, "…") {
			t.Fatalf("wide rune cut misses ellipsis: %q", cut)
		}
	}

	if got := fitLine("abc", 0); got != "" {
		t.Fatalf("fitLine with width 0 = %q, want empty", got)
	}
	if got := truncate("abcdef", 4); lipgloss.Width(got) > 4 || !strings.Contains(got, "…") {
		t.Fatalf("truncate = %q, want a truncated value with ellipsis", got)
	}
	if got := truncate("abcdef", 0); got != "" {
		t.Fatalf("truncate with max 0 = %q, want empty", got)
	}
}

func TestDetailsLongErrorWrapsAndKeepsCause(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := newTestModel(size.width, size.height)
			model.screen = ScreenDetails
			model.details = store.Series{
				ID:    1,
				Title: "Tatort Münster (Thiel und Boerne) - Die Erfindung des Rades",
				Kind:  store.KindTatort,
			}
			model.episodes = []store.KnownEpisode{{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Die Erfindung des Rades"}}}
			model.detailMessage = longDownloadError()

			view := model.View()
			if !containsNormalized(view, longErrorCause) {
				t.Fatalf("details view hides the error cause:\n%s", view)
			}
			if !strings.Contains(view, "esc Bibliothek") {
				t.Fatalf("details view hides the footer:\n%s", view)
			}
			assertFitsWidth(t, view, size.width)
			assertFitsHeight(t, view, size.height)
		})
	}
}

func TestSummaryLongErrorWraps(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := newTestModel(size.width, size.height)
			model.screen = ScreenSummary
			model.jobs = []job{{label: "Folge 1", status: "fertig"}}
			model.doneErr = errors.New(longDownloadError())

			view := model.View()
			if !containsNormalized(view, longErrorCause) {
				t.Fatalf("summary view hides the error cause:\n%s", view)
			}
			for _, want := range []string{"Abgebrochen", "1 von 1 Downloads abgeschlossen.", "Bibliothek", "!"} {
				if !strings.Contains(view, want) {
					t.Fatalf("summary view misses %q:\n%s", want, view)
				}
			}
			assertFitsWidth(t, view, size.width)
			assertFitsHeight(t, view, size.height)
		})
	}
}

func TestStartLongErrorWraps(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := newTestModel(size.width, size.height)
			model.screen = ScreenStart
			model.startErr = errors.New(longDownloadError())

			view := model.View()
			if !containsNormalized(view, longErrorCause) {
				t.Fatalf("start view hides the error cause:\n%s", view)
			}
			if !strings.Contains(view, "starten") {
				t.Fatalf("start view hides the footer:\n%s", view)
			}
			assertFitsWidth(t, view, size.width)
			assertFitsHeight(t, view, size.height)
		})
	}
}

const longErrorCause = "segment 17 returned HTTP 403"

// containsNormalized matches text that may have been wrapped across lines.
func containsNormalized(view, want string) bool {
	borders := strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ")
	return strings.Contains(strings.Join(strings.Fields(borders.Replace(view)), " "), want)
}

func longDownloadError() string {
	return "download failed for Tatort Münster (Thiel und Boerne) - Die Erfindung des Rades (2024): " +
		strings.Repeat("retrying the manifest and media segment fetch; ", 8) +
		longErrorCause
}

func TestEmptyStates(t *testing.T) {
	library := newTestModel(80, 24)
	library.screen = ScreenLibrary
	if view := library.View(); !strings.Contains(view, "Noch keine Serien gespeichert.") ||
		!strings.Contains(view, "Mit a eine URL hinzufügen.") {
		t.Fatalf("empty library state missing:\n%s", view)
	}

	series := newTestModel(80, 24)
	series.screen = ScreenSeries
	if view := series.View(); !strings.Contains(view, "Keine Staffeln verfügbar.") {
		t.Fatalf("empty series state missing:\n%s", view)
	}

	movie := newTestModel(80, 24)
	movie.screen = ScreenDetails
	movie.details = store.Series{Title: "Film", Kind: store.KindMovie}
	if view := movie.View(); !strings.Contains(view, "Film - noch nicht geladen.") {
		t.Fatalf("empty movie state missing:\n%s", view)
	}

	loading := newTestModel(80, 24)
	loading.screen = ScreenDetails
	loading.details = store.Series{Title: "Serie", Kind: store.KindSeries}
	loading.detailMessage = "Folgenliste wird aktualisiert…"
	if view := loading.View(); !strings.Contains(view, "Folgenliste wird aktualisiert.") ||
		strings.Contains(view, "Keine Folgen gespeichert") {
		t.Fatalf("loading detail state wrong:\n%s", view)
	}

	season := newTestModel(80, 24)
	season.screen = ScreenSeasonEpisodes
	season.details = store.Series{Title: "Serie", Kind: store.KindSeries}
	if view := season.View(); !strings.Contains(view, "Keine Folgen in dieser Staffel gespeichert.") {
		t.Fatalf("empty season state missing:\n%s", view)
	}

	queue := newTestModel(80, 16)
	queue.screen = ScreenQueue
	if view := queue.View(); !strings.Contains(view, "Warte auf Download-Meldungen…") {
		t.Fatalf("waiting state missing:\n%s", view)
	}
	queue.jobs = []job{{label: "Folge 1", status: "fertig"}}
	if view := queue.View(); strings.Contains(view, "Warte auf Download-Meldungen…") {
		t.Fatalf("waiting state must disappear after a completed job:\n%s", view)
	}
}

func TestPluralCounts(t *testing.T) {
	queue := newTestModel(120, 30)
	queue.screen = ScreenQueue
	queue.jobs = []job{{label: "Folge 1", status: "fehlgeschlagen"}}
	queue.log = []string{"Fehler beim Laden"}
	if view := queue.View(); !strings.Contains(view, "0 fertig · 1 fehlgeschlagen") {
		t.Fatalf("singular failure count missing:\n%s", view)
	}

	queue.jobs = append(queue.jobs, job{label: "Folge 2", status: "fehlgeschlagen"})
	if view := queue.View(); !strings.Contains(view, "0 fertig · 2 fehlgeschlagen") {
		t.Fatalf("plural failure count missing:\n%s", view)
	}

	details := newTestModel(80, 24)
	details.screen = ScreenDetails
	details.details = store.Series{Title: "Tatort", Kind: store.KindTatort}
	details.episodes = []store.KnownEpisode{
		{EpisodeRef: store.EpisodeRef{ID: "a", Title: "Erste"}},
		{EpisodeRef: store.EpisodeRef{ID: "b", Title: "Zweite"}},
	}
	if view := details.View(); !strings.Contains(view, "2 Folgen, davon 2 offen") {
		t.Fatalf("episode plural missing:\n%s", view)
	}

	seasons := newTestModel(80, 24)
	seasons.screen = ScreenDetails
	seasons.details = store.Series{Title: "Serie", Kind: store.KindSeries}
	seasons.episodes = []store.KnownEpisode{{EpisodeRef: store.EpisodeRef{ID: "a", Season: 1, Number: 1}}}
	seasons.detailSeasons = []detailSeasonSummary{{number: 1, total: 1, open: 1}}
	if view := seasons.View(); !strings.Contains(view, "Staffel 1 - 1 Folge, 1 offen") {
		t.Fatalf("season singular missing:\n%s", view)
	}
}

func newTestModel(width, height int) *Model {
	noop := func(context.Context) error { return nil }
	model := newModel(Options{Config: &config.Options{FileTag: "example"}, Run: noop})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(*Model)
}

func populatedScreen(name string, width, height int) *Model {
	model := newTestModel(width, height)
	longText := strings.Repeat("sehr langer Titel mit vielen Wörtern ", 4)

	switch name {
	case "start":
		model.screen = ScreenStart
	case "library":
		model.screen = ScreenLibrary
		for index := 0; index < 100; index++ {
			entry := store.Series{
				ID:       int64(index + 1),
				Title:    fmt.Sprintf("%s %03d", longText, index),
				Kind:     store.KindSeries,
				Enabled:  index%7 != 0,
				NewCount: index % 5,
			}
			if index%3 == 0 {
				checked := time.Date(2026, 1, 2, 15, 4, 0, 0, time.Local)
				entry.CheckedAt = &checked
			}
			model.library = append(model.library, entry)
		}
		model.libCursor = 60
		model.libOffset = 58
		model.libMessage = "Bibliothek aktualisiert."
	case "series":
		model.screen = ScreenSeries
		model.showName = longText
		model.selection = map[int]bool{}
		for index := 0; index < 30; index++ {
			model.seasons = append(model.seasons, app.Season{Number: index + 1, Title: longText, ID: fmt.Sprintf("s%d", index)})
			model.selection[index+1] = index%2 == 0
		}
		model.cursor = 20
		model.seriesOffset = 18
	case "tatort":
		model.screen = ScreenTatort
		model.tatortCity = "Köln"
		model.tatortCount = 5
		model.skipField.SetValue("0")
		model.skipErr = "Bitte eine Zahl zwischen 0 und 5 eingeben."
	case "details":
		model.screen = ScreenDetails
		model.details = store.Series{ID: 1, Title: longText, URL: "https://www.ardplus.de/details/" + longText, Kind: store.KindSeries}
		model.episodes = []store.KnownEpisode{{EpisodeRef: store.EpisodeRef{ID: "a", Title: longText, Season: 1, Number: 1}}}
		for index := 0; index < 20; index++ {
			model.detailSeasons = append(model.detailSeasons, detailSeasonSummary{number: index + 1, total: index + 1, open: index})
		}
		model.detailSeasonCursor = 15
		model.detailOffset = 13
		model.detailMessage = "Folgenliste wurde aktualisiert."
	case "season episodes":
		model.screen = ScreenSeasonEpisodes
		model.details = store.Series{ID: 1, Title: longText, URL: "https://www.ardplus.de/details/" + longText, Kind: store.KindSeries}
		model.activeDetailSeason = 2
		model.detailChoice = map[int]bool{}
		for index := 0; index < 50; index++ {
			model.detailSeasonEpisodes = append(model.detailSeasonEpisodes, store.KnownEpisode{
				EpisodeRef: store.EpisodeRef{ID: fmt.Sprintf("e%d", index), Title: longText, Season: 2, Number: index + 1},
			})
		}
		model.detailCursor = 30
		model.detailOffset = 28
		model.detailChoice[30] = true
	case "queue":
		model.screen = ScreenQueue
		model.jobs = []job{
			{label: longText, path: "/downloads/" + longText, done: 3, total: 4, status: "lädt"},
			{label: "Fertige Folge", done: 1, total: 1, status: "fertig"},
			{label: "Fehlerhafte Folge", done: 0, total: 1, status: "fehlgeschlagen"},
		}
		for index := 0; index < 20; index++ {
			model.log = append(model.log, fmt.Sprintf("%s Meldung %d", longText, index))
		}
	case "summary":
		model.screen = ScreenSummary
		model.jobs = []job{{label: "Folge 1", status: "fertig"}, {label: "Folge 2", status: "fehlgeschlagen"}}
	case "help":
		model.helpVisible = true
	}
	return model
}

func assertFitsWidth(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line width %d exceeds terminal width %d: %q", got, width, line)
		}
	}
}

func assertFitsHeight(t *testing.T, view string, height int) {
	t.Helper()
	if height <= 0 {
		return
	}
	if lines := len(strings.Split(view, "\n")); lines > height {
		t.Fatalf("view uses %d lines at height %d:\n%s", lines, height, view)
	}
}
