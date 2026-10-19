package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func assertMaxWidth(t *testing.T, label string, lines []string, width int) {
	t.Helper()
	for index, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("%s line %d is %d cells wide, want <= %d: %q", label, index, got, width, ansi.Strip(line))
		}
	}
}

func TestComponentsHeader(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)

	for _, width := range []int{40, 120} {
		lines := renderHeader(width, "Bibliothek", "42 Serien")
		if len(lines) != 2 {
			t.Fatalf("width %d: got %d lines, want 2", width, len(lines))
		}
		assertMaxWidth(t, "header", lines, width)
		first := ansi.Strip(lines[0])
		for _, want := range []string{"ard-plus-dl", "›", "Bibliothek", "42 Serien"} {
			if !strings.Contains(first, want) {
				t.Fatalf("width %d: header %q misses %q", width, first, want)
			}
		}
		if !strings.Contains(lines[0], "\x1b[38;2;") {
			t.Fatalf("width %d: header brand is not rendered with a true-color gradient", width)
		}

		bare := renderHeader(width, "Bibliothek", "")
		if len(bare) != 2 {
			t.Fatalf("width %d: got %d lines without subtitle, want 2", width, len(bare))
		}
		assertMaxWidth(t, "header without subtitle", bare, width)
		bareFirst := ansi.Strip(bare[0])
		if !strings.Contains(bareFirst, "Bibliothek") {
			t.Fatalf("width %d: header without subtitle %q misses title", width, bareFirst)
		}
		if strings.Contains(bareFirst, "42 Serien") {
			t.Fatalf("width %d: header without subtitle %q contains a chip", width, bareFirst)
		}

		wantRule := strings.Repeat("─", width)
		if got := ansi.Strip(lines[1]); got != wantRule {
			t.Fatalf("width %d: rule %q, want %q", width, got, wantRule)
		}
	}
}

func TestComponentsGradientText(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)

	const text = "ard-plus-dl"
	out := gradientText(text)
	if got := ansi.Strip(out); got != text {
		t.Fatalf("gradient text %q strips to %q, want %q", out, got, text)
	}
	if got, want := ansi.StringWidth(out), len(text); got != want {
		t.Fatalf("gradient text is %d cells wide, want %d", got, want)
	}
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Fatalf("gradient text has no true-color escape: %q", out)
	}

	lipgloss.SetColorProfile(termenv.Ascii)
	if got := gradientText(text); got != text {
		t.Fatalf("gradient text without color support = %q, want %q", got, text)
	}
}

func TestComponentsRule(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)

	for _, width := range []int{1, 7, 40} {
		out := renderRule(width)
		if got := ansi.StringWidth(out); got != width {
			t.Fatalf("rule width %d is %d cells", width, got)
		}
		if got, want := ansi.Strip(out), strings.Repeat("─", width); got != want {
			t.Fatalf("rule %q, want %q", got, want)
		}
		if !strings.Contains(out, "\x1b[38;2;") {
			t.Fatalf("rule width %d has no true-color escape", width)
		}
	}
	if got := renderRule(0); got != "" {
		t.Fatalf("zero-width rule = %q, want empty", got)
	}
}

func TestComponentsFooter(t *testing.T) {
	hints := []keyHint{
		{Key: "enter", Label: "Auswählen"},
		{Key: "space", Label: "Markieren"},
		{Key: "/", Label: "Suchen"},
		{Key: "tab", Label: "Bereich wechseln"},
		{Key: "a", Label: "Alle auswählen"},
		{Key: "esc", Label: "Zurück"},
		{Key: "q", Label: "Beenden"},
		{Key: "?", Label: "Hilfe anzeigen"},
		{Key: "r", Label: "Erneut versuchen"},
		{Key: "s", Label: "Einstellungen öffnen"},
	}
	lines := renderFooter(40, hints)
	if len(lines) == 0 || len(lines) > 2 {
		t.Fatalf("got %d lines, want 1 or 2", len(lines))
	}
	assertMaxWidth(t, "footer", lines, 40)
	last := ansi.Strip(lines[len(lines)-1])
	if !strings.Contains(last, "…") {
		t.Fatalf("last footer line %q should end with an ellipsis", last)
	}
	if !strings.Contains(ansi.Strip(lines[0]), "enter") {
		t.Fatalf("first footer line %q misses first hint", ansi.Strip(lines[0]))
	}
}

func TestComponentsPanel(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)

	const width = 40
	lines := []string{"Zeile eins", "Zeile zwei"}

	titled := renderPanel(width, "Staffeln", lines, false)
	if got, want := len(titled), len(lines)+2; got != want {
		t.Fatalf("titled panel has %d lines, want %d", got, want)
	}
	assertMaxWidth(t, "titled panel", titled, width)
	top := ansi.Strip(titled[0])
	if !strings.Contains(top, "Staffeln") {
		t.Fatalf("title is not embedded in the top border: %q", top)
	}
	if !strings.Contains(top, "●") || !strings.Contains(top, "╭") || !strings.Contains(top, "╮") {
		t.Fatalf("top border %q misses marker or corners", top)
	}
	if inner := ansi.Strip(strings.Join(titled[1:len(titled)-1], "\n")); strings.Contains(inner, "Staffeln") {
		t.Fatalf("title should not be an inner line: %q", inner)
	}

	plain := renderPanel(width, "", lines, true)
	if got, want := len(plain), len(lines)+2; got != want {
		t.Fatalf("plain panel has %d lines, want %d", got, want)
	}
	assertMaxWidth(t, "plain panel", plain, width)
	if strings.Contains(ansi.Strip(strings.Join(plain, "\n")), "Staffeln") {
		t.Fatal("plain panel should not contain a title")
	}
	if strings.Join(plain, "\n") == strings.Join(renderPanel(width, "", lines, false), "\n") {
		t.Fatal("focused panel should differ from unfocused panel")
	}
	focusedTitle := renderPanel(width, "Staffeln", lines, true)
	if strings.Join(focusedTitle, "\n") == strings.Join(titled, "\n") {
		t.Fatal("focused title panel should differ from unfocused title panel")
	}

	narrow := renderPanel(12, "Titel", lines, false)
	if len(narrow) != len(lines) {
		t.Fatalf("narrow panel returned %d lines, want %d", len(narrow), len(lines))
	}
	assertMaxWidth(t, "narrow panel", narrow, 12)

	longTitle := renderPanel(24, "Eine sehr lange Panelüberschrift", lines, true)
	assertMaxWidth(t, "long title panel", longTitle, 24)
}

func TestComponentsPillsAndBadge(t *testing.T) {
	cases := []struct {
		kind pillKind
		cue  string
	}{
		{pillNeutral, "●"},
		{pillOpen, "○"},
		{pillDone, "✓"},
		{pillError, "!"},
	}
	for _, tc := range cases {
		out := ansi.Strip(renderPill("Offen", tc.kind))
		if !strings.Contains(out, "Offen") {
			t.Fatalf("pill %q misses text: %q", tc.cue, out)
		}
		if !strings.Contains(out, tc.cue) {
			t.Fatalf("pill %d misses cue %q: %q", tc.kind, tc.cue, out)
		}
	}

	badge := ansi.Strip(renderBadge("HD"))
	if !strings.Contains(badge, "HD") {
		t.Fatalf("badge %q misses text", badge)
	}
}

func TestComponentsProgress(t *testing.T) {
	cases := []struct {
		done, total int
		filled      int
	}{
		{0, 100, 0},
		{50, 100, 10},
		{100, 100, 20},
		{0, 0, 0},
	}
	for _, tc := range cases {
		out := progressBar(tc.done, tc.total, 20)
		if got := ansi.StringWidth(out); got != 20 {
			t.Fatalf("%d/%d: bar is %d cells, want 20", tc.done, tc.total, got)
		}
		if got := strings.Count(out, "█"); got != tc.filled {
			t.Fatalf("%d/%d: %d filled cells, want %d", tc.done, tc.total, got, tc.filled)
		}
		if got := strings.Count(out, "░"); got != 20-tc.filled {
			t.Fatalf("%d/%d: %d empty cells, want %d", tc.done, tc.total, got, 20-tc.filled)
		}
	}

	if got := ansi.StringWidth(progressBar(0, 0, 2)); got != 4 {
		t.Fatalf("narrow bar clamps to 4 cells, got %d", got)
	}
	if got := ansi.StringWidth(progressBar(0, 0, 100)); got != 60 {
		t.Fatalf("wide bar clamps to 60 cells, got %d", got)
	}
}

func ansiForegrounds(value string) map[string]struct{} {
	found := make(map[string]struct{})
	for _, part := range strings.Split(value, "\x1b[38;2;")[1:] {
		if end := strings.IndexByte(part, 'm'); end >= 0 {
			found[part[:end]] = struct{}{}
		}
	}
	return found
}

func TestComponentsProgressGradient(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)

	cases := []struct {
		done, filled int
	}{
		{0, 0},
		{50, 10},
		{100, 20},
	}
	for _, tc := range cases {
		out := progressBar(tc.done, 100, 20)
		if got := ansi.StringWidth(out); got != 20 {
			t.Fatalf("%d%%: bar is %d cells, want 20", tc.done, got)
		}
		if got := strings.Count(out, "█"); got != tc.filled {
			t.Fatalf("%d%%: %d filled cells, want %d", tc.done, got, tc.filled)
		}
		if got := strings.Count(out, "░"); got != 20-tc.filled {
			t.Fatalf("%d%%: %d empty cells, want %d", tc.done, got, 20-tc.filled)
		}

		filledPart := out
		if index := strings.Index(out, "░"); index >= 0 {
			filledPart = out[:index]
		}
		if strings.TrimSpace(ansi.Strip(filledPart)) == "" {
			filledPart = ""
		}
		colors := ansiForegrounds(filledPart)
		if tc.done == 0 && len(colors) != 0 {
			t.Fatalf("empty bar has %d filled colors, want 0", len(colors))
		}
		if tc.done > 0 && len(colors) < 2 {
			t.Fatalf("%d%%: filled part has %d colors, want a gradient", tc.done, len(colors))
		}
	}

	if got := progressBar(10, 0, 20); strings.Contains(got, "█") {
		t.Fatalf("zero total should render an empty bar: %q", got)
	}
}

func TestComponentsStat(t *testing.T) {
	if got, want := ansi.Strip(renderStat("Staffeln", "20")), "20 Staffeln"; got != want {
		t.Fatalf("stat %q, want %q", got, want)
	}
}

func TestComponentsProgressLabel(t *testing.T) {
	cases := []struct {
		done, total int
		want        string
	}{
		{42, 100, "42% (42/100)"},
		{0, 0, "0%"},
		{-1, 0, "0%"},
		{31, 100, "31% (31/100)"},
	}
	for _, tc := range cases {
		if got := progressLabel(tc.done, tc.total); got != tc.want {
			t.Fatalf("progressLabel(%d, %d) = %q, want %q", tc.done, tc.total, got, tc.want)
		}
	}
}

func TestComponentsEmpty(t *testing.T) {
	const width = 40
	lines := renderEmpty(width, "Keine Serien", "Starte mit einer URL.")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	assertMaxWidth(t, "empty", lines, width)
	first := ansi.Strip(lines[0])
	if !strings.Contains(first, "◇") || !strings.Contains(first, "Keine Serien") {
		t.Fatalf("empty heading %q misses symbol or title", first)
	}
	second := ansi.Strip(lines[1])
	if !strings.Contains(second, "Starte mit einer URL.") {
		t.Fatalf("empty hint %q misses text", second)
	}
	if !strings.HasPrefix(first, "     ") || !strings.HasPrefix(second, "     ") {
		t.Fatalf("empty lines are not centered: %q / %q", first, second)
	}
}

func TestComponentsCenterLine(t *testing.T) {
	text := theme.Accent.Render("Mitte")
	out := centerLine(text, 20)
	if got := ansi.StringWidth(out); got != 20 {
		t.Fatalf("centered line is %d cells, want 20", got)
	}
	want := strings.Repeat(" ", 7) + "Mitte" + strings.Repeat(" ", 8)
	if got := ansi.Strip(out); got != want {
		t.Fatalf("centered line %q, want %q", got, want)
	}

	long := centerLine("Deutlich zu langer Text", 10)
	if got := ansi.StringWidth(long); got != 10 {
		t.Fatalf("truncated centered line is %d cells, want 10", got)
	}
	if !strings.Contains(ansi.Strip(long), "…") {
		t.Fatalf("truncated centered line misses ellipsis: %q", ansi.Strip(long))
	}
}

func TestThemeStyles(t *testing.T) {
	if got, want := ansi.StringWidth(theme.Badge.Render("HD")), 4; got != want {
		t.Fatalf("badge width %d, want %d", got, want)
	}
	if got, want := ansi.StringWidth(theme.PillDone.Render("Fertig")), 8; got != want {
		t.Fatalf("pill width %d, want %d", got, want)
	}
	if got := ansi.Strip(renderSection("Downloads")); got != "▍ DOWNLOADS" {
		t.Fatalf("section %q, want %q", got, "▍ DOWNLOADS")
	}
	if got := ansi.Strip(renderStat("Fortschritt", "42%")); got != "42% Fortschritt" {
		t.Fatalf("stat %q, want %q", got, "42% Fortschritt")
	}
	for name, style := range map[string]interface{ Render(...string) string }{
		"Title": theme.Title, "Subtitle": theme.Subtitle, "Muted": theme.Muted,
		"Text": theme.Text, "Accent": theme.Accent, "Label": theme.Label,
		"Success": theme.Success, "Warning": theme.Warning, "Danger": theme.Danger,
		"Info": theme.Info, "Selected": theme.Selected, "PanelTitle": theme.PanelTitle,
		"InputPrompt": theme.InputPrompt, "InputText": theme.InputText,
		"InputPlaceholder": theme.InputPlaceholder, "InputCursor": theme.InputCursor,
		"Key": theme.Key, "KeyLabel": theme.KeyLabel, "Divider": theme.Divider,
	} {
		if strings.TrimSpace(ansi.Strip(style.Render("x"))) != "x" {
			t.Fatalf("style %s did not render its content", name)
		}
	}
}
