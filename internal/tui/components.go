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
// and a gradient rule.
func renderHeader(width int, title, subtitle string) []string {
	if width <= 0 {
		return []string{"", ""}
	}
	head := gradientText("ard-plus-dl") + theme.Muted.Render(" › ") + theme.Title.Render(title)

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

// renderRule returns a gradient horizontal line of exactly width cells.
func renderRule(width int) string {
	if width <= 0 {
		return ""
	}
	start, end := adaptiveHex(colorAccent), adaptiveHex(colorBorder)
	var builder strings.Builder
	for index := 0; index < width; index++ {
		color := interpolateHex(start, end, index, width)
		builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("─"))
	}
	return builder.String()
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
	if lipgloss.HasDarkBackground() {
		return colorGradientStartDark, colorGradientEndDark
	}
	return colorGradientStartLight, colorGradientEndLight
}

// adaptiveHex resolves an adaptive color for the active background.
func adaptiveHex(color lipgloss.AdaptiveColor) string {
	if lipgloss.HasDarkBackground() {
		return color.Dark
	}
	return color.Light
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

// renderFooter renders key hints and wraps them onto at most two lines.
func renderFooter(width int, hints []keyHint) []string {
	if width <= 0 {
		return []string{""}
	}
	const gap = "   "
	rendered := make([]string, 0, len(hints))
	for _, hint := range hints {
		rendered = append(rendered, theme.Key.Render(hint.Key)+" "+theme.KeyLabel.Render(hint.Label))
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

// renderPanel draws lines inside a rounded border. The title sits in the top
// border; focused switches the border to the accent color.
func renderPanel(width int, title string, lines []string, focused bool) []string {
	if width <= 0 || width < 24 {
		out := make([]string, len(lines))
		for index, line := range lines {
			out[index] = fitToWidth(line, width)
		}
		return out
	}

	borderColor := colorBorder
	if focused {
		borderColor = colorAccent
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	inner := width - 4 // border (2) and horizontal padding (2)

	var top string
	if title != "" {
		label := fitToWidth(title, width-7) // "╭─ ● " and the closing " ╮"
		titlePart := theme.Accent.Render("●") + " " + theme.PanelTitle.Render(label)
		fill := width - 5 - ansi.StringWidth(titlePart)
		if fill < 0 {
			fill = 0
		}
		top = border.Render("╭─ ") + titlePart + border.Render(" "+strings.Repeat("─", fill)+"╮")
	} else {
		top = border.Render("╭" + strings.Repeat("─", width-2) + "╮")
	}

	out := make([]string, 0, len(lines)+2)
	out = append(out, top)
	for _, line := range lines {
		fitted := fitToWidth(line, inner)
		padding := inner - ansi.StringWidth(fitted)
		if padding < 0 {
			padding = 0
		}
		out = append(out, border.Render("│")+" "+fitted+strings.Repeat(" ", padding)+" "+border.Render("│"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return out
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
	start, end := adaptiveHex(colorAccent), adaptiveHex(colorSuccess)
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

// renderSection renders an uppercase accent heading with a block marker.
func renderSection(title string) string {
	return theme.Accent.Bold(true).Render("▍ " + strings.ToUpper(title))
}
