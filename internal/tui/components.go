package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// keyHint is one shortcut shown in the footer.
type keyHint struct {
	Key   string
	Label string
}

// pillKind selects the badge style and the non-color cue symbol.
type pillKind int

// Pill kinds.
const (
	pillNeutral pillKind = iota
	pillOpen
	pillDone
	pillError
)

// fitToWidth truncates styled text to width cells, marking the cut with "…".
func fitToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

// renderHeader returns the brand/title line with a right-aligned status chip
// and a rule below it. The status shows what the app is doing right now.
func renderHeader(width int, title, subtitle, status string) []string {
	if width <= 0 {
		return []string{"", ""}
	}
	head := gradientText("ard-plus-dl") + theme.Muted.Render(" › ") + theme.Title.Render(title)
	if status != "" {
		head += "  " + theme.Accent.Render(fitToWidth(status, 48))
	}

	line := head
	if subtitle != "" {
		room := width - ansi.StringWidth(head) - 2 // chip padding and spacing
		if room >= 6 {
			text := fitToWidth(subtitle, room-2)
			if strings.TrimSpace(ansi.Strip(text)) != "" {
				chip := theme.Badge.Render(text)
				line = head + strings.Repeat(" ", width-ansi.StringWidth(head)-ansi.StringWidth(chip)) + chip
			}
		}
	}
	return []string{fitToWidth(line, width), renderRule(width)}
}

// renderRule returns a horizontal rule of exactly width cells.
func renderRule(width int) string {
	if width <= 0 {
		return ""
	}
	return theme.Accent.Render(strings.Repeat("─", width))
}

// gradientText colors text with an RGB gradient from teal to violet. Runes are
// interpolated individually so the output keeps the original cell width.
func gradientText(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	start, end := gradientEndpoints()
	var builder strings.Builder
	for index, value := range runes {
		color := interpolateHex(start, end, index, len(runes))
		builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(string(value)))
	}
	return builder.String()
}

// gradientEndpoints picks palette entries that stay legible on the terminal
// background.
func gradientEndpoints() (string, string) {
	if isDark() {
		return colorGradientStartDark, colorGradientEndDark
	}
	return colorGradientStartLight, colorGradientEndLight
}

// interpolateHex mixes two hex colors; index runs from 0 to count-1.
func interpolateHex(start, end string, index, count int) string {
	startR, startG, startB := parseHexColor(start)
	endR, endG, endB := parseHexColor(end)
	if count <= 1 {
		return formatHexColor(startR, startG, startB)
	}
	weight := count - 1
	mix := func(from, to int) int {
		return (from*weight + (to-from)*index) / weight
	}
	return formatHexColor(mix(startR, endR), mix(startG, endG), mix(startB, endB))
}

func parseHexColor(value string) (int, int, int) {
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(parsed>>16) & 0xFF, int(parsed>>8) & 0xFF, int(parsed) & 0xFF
}

func formatHexColor(red, green, blue int) string {
	return fmt.Sprintf("#%02X%02X%02X", red, green, blue)
}

// renderFooter renders key hints as keycaps and wraps them onto at most two
// lines.
func renderFooter(width int, hints []keyHint) []string {
	if width <= 0 {
		return []string{""}
	}
	const gap = "   "
	rendered := make([]string, 0, len(hints))
	for _, hint := range hints {
		rendered = append(rendered, renderKeycap(hint.Key)+" "+theme.KeyLabel.Render(hint.Label))
	}
	if len(rendered) == 0 {
		return []string{""}
	}

	current := rendered[0]
	for index, hint := range rendered[1:] {
		if ansi.StringWidth(current+gap+hint) <= width {
			current += gap + hint
			continue
		}
		// Second line: fit what still fits and truncate the remainder.
		rest := strings.Join(rendered[index+1:], gap)
		return []string{fitToWidth(current, width), fitToWidth(rest, width)}
	}
	return []string{fitToWidth(current, width)}
}

// renderPopup draws a box with the given lines and returns the rows of a
// window that floats over the page. The window is centered.
func renderPopup(width int, title string, lines []string) []string {
	inner := width
	for _, line := range lines {
		if size := ansi.StringWidth(line) + 4; size > inner {
			inner = size
		}
	}
	inner = min(inner, max(20, width-4))

	rows := []string{theme.Muted.Render("  " + title)}
	rows = append(rows, "")
	for _, line := range lines {
		rows = append(rows, "  "+fitToWidth(line, inner-4))
	}
	rows = append(rows, "")
	rows = append(rows, theme.Muted.Render("  esc schließen"))

	box := inner + 2
	offset := max(0, (width-box)/2)

	out := make([]string, 0, len(rows)+2)
	out = append(out, strings.Repeat(" ", offset)+theme.Accent.Render("╭"+strings.Repeat("─", inner)+"╮"))
	for _, row := range rows {
		filled := fitToWidth(row, inner-2)
		padding := inner - 2 - ansi.StringWidth(filled)
		if padding < 0 {
			padding = 0
		}
		line := theme.Accent.Render("│") + theme.Popup.Render(" "+filled+strings.Repeat(" ", padding)+" ") + theme.Accent.Render("│")
		out = append(out, strings.Repeat(" ", offset)+line)
	}
	out = append(out, strings.Repeat(" ", offset)+theme.Accent.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

// overlay places a window in the middle of the rendered page.
func overlay(lines, window []string) []string {
	if len(window) == 0 {
		return lines
	}
	page := make([]string, len(lines))
	copy(page, lines)
	top := max(0, (len(page)-len(window))/2-1)
	for index, row := range window {
		position := top + index
		if position < 0 || position >= len(page) {
			continue
		}
		page[position] = row
	}
	return page
}

// renderKeycap renders a shortcut as a small chip.
func renderKeycap(key string) string {
	return theme.Keycap.Render(key)
}

// renderCard draws a flat card: a title row with an optional right aligned
// meta value, a rule underneath and the indented content. Cards replace the
// old framed panels, so the content uses the full width and the screen reads
// as a flat surface instead of a stack of boxes.
func renderCard(width int, title string, lines []string, focused bool, meta string) []string {
	if width <= 0 {
		return nil
	}
	if width < minCardWidth {
		// Too narrow for a card: keep the content only.
		out := make([]string, len(lines))
		for index, line := range lines {
			out[index] = fitToWidth(line, width)
		}
		return out
	}

	out := make([]string, 0, len(lines)+2)
	out = append(out, fitToWidth(cardTitleLine(width, title, meta, focused), width))
	out = append(out, theme.Divider.Render(strings.Repeat("─", width)))
	for _, line := range lines {
		out = append(out, fitToWidth("  "+line, width))
	}
	return out
}

// cardTitleLine renders the title row of a card with the meta value aligned
// to the right.
func cardTitleLine(width int, title, meta string, focused bool) string {
	style := theme.CardTitle
	if !focused {
		style = theme.Muted.Bold(true)
	}
	head := theme.Accent.Render("▍") + " " + style.Render(title)
	if meta == "" {
		return head
	}
	metaWidth := ansi.StringWidth(meta)
	if width-ansi.StringWidth(head)-metaWidth < 1 {
		return head
	}
	return head + strings.Repeat(" ", width-ansi.StringWidth(head)-metaWidth) + theme.Muted.Render(meta)
}

// renderNotice renders a status line below a card. Errors keep the danger
// color, everything else stays muted.
func renderNotice(message string) string {
	if message == "" {
		return ""
	}
	style := theme.Muted
	marker := theme.Accent.Render("●")
	if dangerText(message) {
		style = theme.Danger
		marker = theme.Danger.Render("●")
	}
	return marker + " " + style.Render(message)
}

// renderPill applies the matching pill style plus a shape cue, so the kind is
// readable without color.
func renderPill(text string, kind pillKind) string {
	switch kind {
	case pillOpen:
		return theme.PillOpen.Render("○ " + text)
	case pillDone:
		return theme.PillDone.Render("✓ " + text)
	case pillError:
		return theme.PillError.Render("! " + text)
	default:
		return theme.PillNeutral.Render("● " + text)
	}
}

// renderBadge renders a compact label.
func renderBadge(text string) string {
	return theme.Badge.Render(text)
}

// progressBar renders a filled/empty bar without a percentage label.
func progressBar(done, total, width int) string {
	if width < 4 {
		width = 4
	}
	if width > 60 {
		width = 60
	}
	filled := 0
	if total > 0 {
		filled = done * width / total
	}
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	start, end := themeHex(colorAccent), themeHex(colorSuccess)
	var builder strings.Builder
	for index := 0; index < filled; index++ {
		color := interpolateHex(start, end, index, filled)
		builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("█"))
	}
	builder.WriteString(theme.Divider.Render(strings.Repeat("░", width-filled)))
	return builder.String()
}

// progressLabel renders the percentage and the raw counts.
func progressLabel(done, total int) string {
	if total <= 0 {
		return "0%"
	}
	percent := done * 100 / total
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%d%% (%d/%d)", percent, done, total)
}

// renderEmpty centers a symbol, title and hint in the available width.
func renderEmpty(width int, title, hint string) []string {
	if width <= 0 {
		return []string{"", ""}
	}
	head := theme.Accent.Render("◇") + " " + theme.Title.Render(title)
	lines := []string{fitToWidth(centerLine(head, width), width)}
	if hint != "" {
		lines = append(lines, fitToWidth(centerLine(theme.Muted.Render(hint), width), width))
	}
	return lines
}

// renderEmptyAction centers a symbol, title, hint and the key that leads
// out of the empty state.
func renderEmptyAction(width int, title, hint, key, action string) []string {
	if width <= 0 {
		return []string{"", ""}
	}
	lines := renderEmpty(width, title, hint)
	if key == "" {
		return lines
	}
	prompt := renderKeycap(key) + " " + theme.KeyLabel.Render(action)
	return append(lines, fitToWidth(centerLine(prompt, width), width))
}

// renderSkeleton draws placeholder rows for content that is still loading.
func renderSkeleton(width, rows int) []string {
	if rows <= 0 || width <= 0 {
		return nil
	}
	lines := make([]string, 0, rows)
	for index := range rows {
		length := width - 6
		if index == rows-1 && length > 8 {
			length = length / 2
		}
		if length < 4 {
			length = 4
		}
		lines = append(lines, theme.Muted.Render("  "+strings.Repeat("░", length)))
	}
	return lines
}

// renderStat renders a bold value followed by its muted label.
func renderStat(label, value string) string {
	return theme.Accent.Bold(true).Render(value) + " " + theme.Muted.Render(label)
}

// centerLine pads text with spaces so its visible cells sit centered.
func centerLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	textWidth := ansi.StringWidth(text)
	if textWidth >= width {
		return fitToWidth(text, width)
	}
	left := (width - textWidth) / 2
	right := width - textWidth - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

// renderSection renders an uppercase accent heading with a small marker.
func renderSection(title string) string {
	return theme.Accent.Bold(true).Render("· " + strings.ToUpper(title))
}
