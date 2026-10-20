package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const maxContentWidth = 120

// View renders the current screen.
func (m *Model) View() string {
	if m.helpVisible {
		return m.helpView()
	}
	if m.width > 0 && (m.width < 40 || (m.height > 0 && m.height < 12)) {
		return fitLine(centerLine("Terminal zu klein (mindestens 40x12).", m.width), m.width)
	}
	switch m.screen {
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
	case ScreenDetails:
		return m.detailsView()
	case ScreenSeasonEpisodes:
		return m.seasonEpisodesView()
	}
	return ""
}

// contentWidth keeps the layout readable on very wide terminals.
func (m *Model) contentWidth() int {
	if m.width > 0 && m.width < maxContentWidth {
		return m.width
	}
	return maxContentWidth
}

// frameLines fits every line to the content width and clips the height.
func (m *Model) frameLines(lines []string) string {
	width := m.contentWidth()
	fitted := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted = append(fitted, fitLine(line, width))
	}
	if m.height > 0 && len(fitted) > m.height {
		fitted = fitted[:m.height]
	}
	return strings.Join(fitted, "\n")
}

// renderFrame joins header, body and footer and keeps the footer visible when
// the terminal is too short for everything.
func (m *Model) renderFrame(header, body, footer []string) string {
	if m.height > 0 {
		overflow := len(header) + len(body) + len(footer) - m.height
		if overflow > 0 {
			if overflow >= len(body) {
				body = nil
			} else {
				body = body[:len(body)-overflow]
			}
		}
	}
	lines := make([]string, 0, len(header)+len(body)+len(footer))
	lines = append(lines, header...)
	lines = append(lines, body...)
	lines = append(lines, footer...)
	return m.frameLines(lines)
}

// panelInner is the content width inside a bordered panel. The two cells of
// slack cover the optional panel padding.
func panelInner(width int) int {
	return max(1, width-4)
}

func panelOverhead(width int, title string) int {
	return len(renderPanel(width, title, nil, false))
}

// listBudget returns the number of list rows that fit next to the fixed chrome.
func listBudget(height int, header, footer []string, extra int, width int, panelTitle string) int {
	if height <= 0 {
		return 1 << 30
	}
	available := height - len(header) - len(footer) - extra - panelOverhead(width, panelTitle)
	return max(0, available)
}

// clampLines keeps at most budget rows, so a panel never grows past its slot.
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
