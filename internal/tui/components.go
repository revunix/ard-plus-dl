package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

// The characters of the boxes: thin lines with round corners, the title sits
// in the upper line.
const (
	boxHorizontal  = "─"
	boxVertical    = "│"
	boxTopLeft     = "╭"
	boxTopRight    = "╮"
	boxBottomLeft  = "╰"
	boxBottomRight = "╯"
	// cursorBar marks the line under the cursor.
	cursorBar = "▌ "
	// meterSymbol fills the progress bar.
	meterSymbol = "■"
	// meterBackground is the empty part of the progress bar.
	meterBackground = "·"
	// maxBarWidth is the widest progress bar.
	maxBarWidth = 160
)

// headerClock returns the time in the top bar. Tests replace it so that the
// output stays the same.
var headerClock = func() string { return time.Now().Format("15:04") }

// renderHeader returns the head line: on the left the name of the program,
// the title of the view and the running action; on the right the context and
// the clock. A quiet rule closes the head.
func renderHeader(width int, title, subtitle, status string) []string {
	if width <= 0 {
		return []string{"", ""}
	}
	separator := theme.Divider.Render("  │  ")

	head := gradientText("ard-plus-dl")
	if title != "" {
		head += separator + theme.Title.Render(title)
	}
	if status != "" {
		head += "   " + theme.Accent.Render(fitToWidth(status, 48))
	}

	// On the right the context of the view and the clock. The clock only
	// stands in terminals wide enough to keep the title readable.
	clock := ""
	if width >= 60 {
		clock = separator + theme.Text.Render(headerClock())
	}
	right := ""
	if strings.TrimSpace(ansi.Strip(subtitle)) != "" {
		// The context gives way first: it is cut to the room beside the
		// title and disappears when hardly anything of it would be left.
		room := width - ansi.StringWidth(head) - 2 - ansi.StringWidth(clock)
		if room >= 8 {
			right = fitToWidth(theme.Muted.Render(subtitle), room)
		}
	}
	if clock != "" {
		if right == "" {
			clock = strings.TrimPrefix(clock, separator)
		}
		right += clock
	}
	if room := width - ansi.StringWidth(right) - 2; ansi.StringWidth(head) > room {
		head = fitToWidth(head, max(1, room))
	}
	line := head
	if right != "" {
		gap := max(1, width-ansi.StringWidth(head)-ansi.StringWidth(right))
		line += strings.Repeat(" ", gap) + right
	}
	return []string{fitToWidth(line, width), renderRule(width)}
}

// renderRule returns a horizontal rule of exactly width cells.
func renderRule(width int) string {
	if width <= 0 {
		return ""
	}
	return theme.Divider.Render(strings.Repeat(boxHorizontal, width))
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
	// The key bright and bold, the label muted, a dot between the pairs.
	gap := theme.Subtle.Render("  ·  ")
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

// windowPadding is the room between the edge of a window and its text.
const windowPadding = 2

// renderPopup draws a window that floats over the page: a raised surface with
// a thin strip in the brand color on top, the title in the first line and the
// key that closes the window on the right. Half blocks give the window sharp
// edges. The width includes the padding on both sides.
func renderPopup(width int, title string, lines []string) []string {
	return renderWindow(width, title, "esc", lines)
}

// renderWindow draws the floating window and names the key that closes it in
// the upper right corner; an empty close leaves the corner free.
func renderWindow(width int, title, close string, lines []string) []string {
	inner := 0
	for _, line := range lines {
		inner = max(inner, ansi.StringWidth(line))
	}
	inner = max(inner, ansi.StringWidth(title)+6)
	inner = min(inner, max(20, width-2*windowPadding))
	full := inner + 2*windowPadding
	pad := strings.Repeat(" ", windowPadding)

	row := func(text string) string {
		text = fitToWidth(text, inner)
		text += strings.Repeat(" ", max(0, inner-ansi.StringWidth(text)))
		return onSurface(pad+text+pad, theme.Popup)
	}
	label := fitToWidth(theme.Title.Render(title), max(1, inner-5))
	gap := max(1, inner-ansi.StringWidth(label)-ansi.StringWidth(close))
	body := []string{row(label + strings.Repeat(" ", gap) + theme.Subtle.Render(close))}
	body = append(body, row(""))
	for _, line := range lines {
		body = append(body, row(line))
	}
	body = append(body, row(""))

	page := backgroundSequence()
	top := withBackground(theme.Frame.Render(strings.Repeat("▄", full)), page)
	bottom := withBackground(theme.WindowEdge.Render(strings.Repeat("▀", full)), page)
	return append(append([]string{top}, body...), bottom)
}

// dimText lays the scrim over a part of the page: the colors fall away and
// the text stands in one quiet color. The page stays readable behind an open
// window, but steps back.
func dimText(text string) string {
	if text == "" {
		return ""
	}
	return withBackground(theme.Scrim.Render(ansi.Strip(text)), backgroundSequence())
}

// overlay places a window in the middle of the rendered page. The page
// behind the window steps back under the scrim.
func overlay(lines, window []string) []string {
	if len(window) == 0 {
		return lines
	}
	page := make([]string, len(lines))
	for index, line := range lines {
		page[index] = dimText(line)
	}
	pageWidth, windowWidth := 0, 0
	for _, line := range lines {
		pageWidth = max(pageWidth, ansi.StringWidth(line))
	}
	for _, line := range window {
		windowWidth = max(windowWidth, ansi.StringWidth(line))
	}
	left := max(0, (pageWidth-windowWidth)/2)
	top := max(0, (len(page)-len(window))/2)
	for index, row := range window {
		position := top + index
		if position < 0 || position >= len(page) {
			continue
		}
		// The page stays visible left and right of the window, under the
		// scrim like the rest of it.
		line := lines[position]
		before := ansi.Cut(line, 0, left)
		before += strings.Repeat(" ", max(0, left-ansi.StringWidth(before)))
		page[position] = dimText(before) + row + dimText(ansi.Cut(line, left+windowWidth, pageWidth))
	}
	return page
}

// renderKeycap renders a key: bright and bold, so it stands out from the
// muted label beside it.
func renderKeycap(key string) string {
	return theme.Keycap.Render(key)
}

// renderCard draws a box with thin lines and round corners around the
// content. The title stands in the upper line, the meta value muted behind
// it. The box the keys work in carries the brand color, the others stand
// back in a quiet grey.
func renderCard(width int, title string, lines []string, focused bool, meta string) []string {
	if width <= 0 {
		return nil
	}
	if width < minCardWidth {
		// Too narrow for a box: keep the content only.
		out := make([]string, len(lines))
		for index, line := range lines {
			out[index] = fitToWidth(line, width)
		}
		return out
	}

	frame := theme.FrameIdle
	if focused {
		frame = theme.Frame
	}
	inner := cardInner(width)
	out := make([]string, 0, len(lines)+2)
	out = append(out, cardTitleLine(width, title, meta, frame))
	for _, line := range lines {
		text := fitToWidth(line, inner)
		padding := max(0, inner-ansi.StringWidth(text))
		out = append(out, frame.Render(boxVertical)+" "+text+strings.Repeat(" ", padding)+" "+frame.Render(boxVertical))
	}
	out = append(out, frame.Render(boxBottomLeft+strings.Repeat(boxHorizontal, width-2)+boxBottomRight))
	return out
}

// cardTitleLine renders the upper line of a box: the title bright, the meta
// value muted behind it, the line runs on up to the corner.
func cardTitleLine(width int, title, meta string, frame lipgloss.Style) string {
	top := frame.Render(boxTopLeft + boxHorizontal)
	if title != "" {
		label := " " + theme.CardTitle.Render(title)
		if meta != "" {
			label += " " + theme.Muted.Render(meta)
		}
		label += " "
		if room := width - 4; ansi.StringWidth(label) > room {
			label = fitToWidth(label, max(1, room))
		}
		top += label
	}
	rest := max(0, width-ansi.StringWidth(top)-1)
	return top + frame.Render(strings.Repeat(boxHorizontal, rest)+boxTopRight)
}

// padLines fills the content of a box with empty lines up to the height, so
// boxes side by side end on the same line.
func padLines(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

// cursorMark returns the room in front of a line of a list: the line under
// the cursor carries the bar in the brand color there.
func cursorMark(active bool) string {
	if active {
		return theme.Cursor.Render(cursorBar)
	}
	return "  "
}

// highlightRow lays the background of the marked line over the full width.
func highlightRow(line string, width int) string {
	line = fitToWidth(line, width)
	line += strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	return onSurface(line, theme.Row)
}

// onSurface lays the background of a style under a rendered line. The reset
// at the end hands the rest of the row back to the surface of the page.
func onSurface(line string, surface lipgloss.Style) string {
	background := sequenceOf(surface)
	if background == "" {
		return line
	}
	return withBackground(line, background) + "\x1b[0m"
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
	if width > maxBarWidth {
		width = maxBarWidth
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
	// The color runs over the full width from the brand blue to the cyan,
	// the empty part is dotted.
	start, end := gradientEndpoints()
	var builder strings.Builder
	for index := 0; index < filled; index++ {
		color := interpolateHex(start, end, index, width)
		builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(meterSymbol))
	}
	builder.WriteString(theme.FrameIdle.Render(strings.Repeat(meterBackground, width-filled)))
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
