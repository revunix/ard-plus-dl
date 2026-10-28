package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const maxContentWidth = 120

// minCardWidth is the smallest width that still carries a card title and rule.
const minCardWidth = 24

// minSplitWidth is the smallest width that carries two columns side by side.
const minSplitWidth = 92

// columnGap is the number of empty cells between two columns.
const columnGap = 2

// View renders the current screen.
func (m *Model) View() string {
	if m.width > 0 && (m.width < 40 || (m.height > 0 && m.height < 12)) {
		return fitLine(centerLine("Terminal zu klein (mindestens 40x12).", m.width), m.width)
	}
	page := m.pageView()
	if m.helpVisible {
		// The help floats over the page instead of replacing it.
		page = overlay(page, m.helpPopup())
	}
	return strings.Join(page, "\n")
}

// pageView renders the screen without the help popup.
func (m *Model) pageView() []string {
	switch m.screen {
	case ScreenSplash:
		return m.splashView()
	case ScreenEditEntry:
		return m.editEntryView()
	case ScreenStart:
		return m.startView()
	case ScreenSeries:
		return m.seriesView()
	case ScreenTatort:
		return m.tatortView()
	case ScreenQueue, ScreenLoading:
		return m.queueView()
	case ScreenSummary:
		return m.summaryView()
	case ScreenLibrary:
		return m.libraryView()
	case ScreenCatalog:
		return m.catalogView()
	case ScreenDetails:
		return m.detailsView()
	case ScreenSeasonEpisodes:
		return m.seasonEpisodesView()
	}
	return nil
}

// paintRows fits every line to the terminal width and paints the surface
// color of the color scheme behind it.
func (m *Model) paintRows(lines []string) []string {
	inner := m.frameWidth()
	width := inner
	margin := ""
	if m.width > 0 {
		width = m.width
		margin = strings.Repeat(" ", (width-inner)/2)
	}
	background := backgroundSequence()
	painted := make([]string, 0, len(lines))
	for _, line := range lines {
		line = margin + fitLine(line, inner)
		if background != "" {
			padding := width - ansi.StringWidth(line)
			if padding > 0 {
				line += strings.Repeat(" ", padding)
			}
			line = withBackground(line, background)
		}
		painted = append(painted, line)
	}
	if m.height > 0 && len(painted) > m.height {
		painted = painted[:m.height]
	}
	return painted
}

// withBackground puts the surface color behind a rendered line. Every reset
// inside the line starts the surface color again.
func withBackground(line, background string) string {
	if background == "" {
		return line
	}
	return strings.ReplaceAll(background+line, "\x1b[0m", "\x1b[0m"+background)
}

// frameRows joins header, body and footer into the rows of the screen. The
// body is trimmed so that the footer stays visible and filled up so that the
// footer always sits at the bottom of the terminal. Every row carries the
// surface color of the color scheme.
func (m *Model) frameRows(header, body, footer []string) []string {
	if m.height > 0 {
		available := m.height - len(header) - len(footer)
		if available < 0 {
			available = 0
		}
		if len(body) > available {
			body = body[:available]
		}
		for len(body) < available {
			body = append(body, "")
		}
	}
	lines := make([]string, 0, len(header)+len(body)+len(footer))
	lines = append(lines, header...)
	lines = append(lines, body...)
	lines = append(lines, footer...)
	return m.paintRows(lines)
}

// cardInner is the content width inside a box: the frame and one cell of
// room on each side.
func cardInner(width int) int {
	return max(1, width-4)
}

// boxHeight returns the number of content lines a box gets when it fills the
// body between header and footer. The reserved lines stay free below the box.
func (m *Model) boxHeight(header, footer []string, reserved int) int {
	if m.height <= 0 {
		return 0
	}
	return max(0, m.height-len(header)-len(footer)-reserved-2)
}

// cardOverhead returns the rows a box spends on its upper and lower line.
func cardOverhead(width int) int {
	if width < minCardWidth {
		return 0
	}
	return 2
}

// listBudget returns the number of list rows that fit next to the fixed chrome.
func listBudget(height int, header, footer []string, extra int, width int) int {
	if height <= 0 {
		return 1 << 30
	}
	available := height - len(header) - len(footer) - extra - cardOverhead(width)
	return max(0, available)
}

// splitWidths splits the width into two columns. The first column gets the
// given share of the free space.
func splitWidths(width int, share float64) (int, int) {
	free := width - columnGap
	if free < 20 {
		return width, 0
	}
	left := int(float64(free) * share)
	left = min(max(left, 20), free-20)
	return left, free - left
}

// splitColumns renders two blocks of lines next to each other.
func splitColumns(left, right []string, leftWidth, rightWidth int) []string {
	if rightWidth <= 0 {
		return left
	}
	return strings.Split(joinColumns(left, right, leftWidth, rightWidth), "\n")
}

// clampLines keeps at most budget rows, so a card never grows past its slot.
func clampLines(lines []string, budget int) []string {
	if budget < 0 {
		budget = 0
	}
	if len(lines) > budget {
		return lines[:budget]
	}
	return lines
}

func fitInner(lines []string, width int) []string {
	for index, line := range lines {
		lines[index] = fitLine(line, width)
	}
	return lines
}

func dangerText(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "fehler") || strings.Contains(lower, "error") ||
		strings.Contains(lower, "konnte nicht") || strings.Contains(lower, "fehlgeschlagen")
}

func tailWithEllipsis(lines []string, budget int) []string {
	if budget >= len(lines) {
		return lines
	}
	if budget <= 0 {
		return nil
	}
	tail := append([]string(nil), lines[len(lines)-budget:]...)
	tail[0] = "… " + tail[0]
	return tail
}

func joinColumns(left, right []string, leftWidth, rightWidth int) string {
	rows := max(len(left), len(right))
	var b strings.Builder
	for index := range rows {
		leftLine, rightLine := "", ""
		if index < len(left) {
			leftLine = fitLine(left[index], leftWidth)
		}
		if index < len(right) {
			rightLine = fitLine(right[index], rightWidth)
		}
		padding := leftWidth - ansi.StringWidth(leftLine)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(leftLine + strings.Repeat(" ", padding) + "  " + rightLine)
		if index+1 < rows {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

// wrapText wraps value to width, preserving ANSI sequences and wide runes.
func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	return strings.Split(ansi.Wrap(value, width, " /-"), "\n")
}

// fitLogLine keeps the end of a log line so the actual error cause stays visible.
func fitLogLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	valueWidth := ansi.StringWidth(value)
	if valueWidth <= width {
		return value
	}
	trim := valueWidth - width + ansi.StringWidth("…")
	return ansi.TruncateLeft(value, trim, "…")
}

func (m *Model) tailLog(count int) []string {
	if len(m.log) <= count {
		return m.log
	}
	return m.log[len(m.log)-count:]
}

// windowRange returns the visible slice and whether list markers are needed.
func windowRange(total, cursor, offset, budget int) (start, end int, top, bottom bool) {
	if total <= 0 || budget <= 0 {
		return 0, 0, false, false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		offset = total - 1
	}
	start = offset
	if cursor < start {
		start = cursor
	}
	end = start + budget
	if end > total {
		end = total
	}
	if cursor >= end {
		end = cursor + 1
		if end > total {
			end = total
		}
		start = end - budget
		if start < 0 {
			start = 0
		}
	}
	top = start > 0
	bottom = end < total
	if budget < 3 {
		// With one or two rows, the cursor and the rows matter more than the
		// "more" markers.
		top = false
		bottom = false
	}
	for end-start+markerCount(top, bottom) > budget {
		if end > cursor+1 {
			end--
			bottom = end < total
			continue
		}
		if bottom {
			bottom = false
			continue
		}
		if top {
			top = false
			continue
		}
		break
	}
	return start, end, top, bottom
}

func markerCount(top, bottom bool) int {
	count := 0
	if top {
		count++
	}
	if bottom {
		count++
	}
	return count
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	return fitLine(value, max)
}

func fileWord(count int) string {
	if count == 1 {
		return "Datei"
	}
	return "Dateien"
}

func episodeWord(count int) string {
	if count == 1 {
		return "Folge"
	}
	return "Folgen"
}

// entryWord names a number of library entries.
func entryWord(count int) string {
	if count == 1 {
		return "Eintrag"
	}
	return "Einträge"
}
