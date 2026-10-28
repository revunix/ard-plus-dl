package tui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
)

// The lists stand in the upper part of the screen; below them, like the
// boxes of btop, the protocol of the session and an overview of the library
// and the settings.

// lowerMinHeight is the terminal height from which the lower boxes show.
const lowerMinHeight = 24

// lowerRows returns the number of rows the lower boxes take, the free line
// above them included. Narrow and low terminals keep the lists alone.
func (m *Model) lowerRows() int {
	if m.height < lowerMinHeight {
		return 0
	}
	if _, right := m.splitAt(m.frameWidth(), 0.55); right <= 0 {
		return 0
	}
	// The head takes three rows: the line, the rule and the free line.
	free := m.height - 3 - m.footerHeight(m.frameWidth(), m.screenHints())
	return max(6, free/3) + 1
}

// screenHints returns the keys the footer of the current screen shows.
func (m *Model) screenHints() []keyHint {
	return append(m.hints().short, keyHint{Key: "?", Label: "Hilfe"})
}

// lowerBoxes renders the protocol and the overview side by side. They stand
// above the footer, so the lists above take the rest of the height.
func (m *Model) lowerBoxes(width int) []string {
	rows := m.lowerRows()
	if rows <= 0 {
		return nil
	}
	left, right := m.splitAt(width, 0.55)
	content := rows - 3
	protocol := renderCard(left, "Protokoll", padLines(m.protocolLines(cardInner(left), content), content), false, "")
	overview := renderCard(right, "Übersicht", padLines(clampLines(m.overviewLines(cardInner(right)), content), content), false, "")
	return append([]string{""}, splitColumns(protocol, overview, left, right)...)
}

// protocolLines renders the newest messages of the session with the time
// they came in.
func (m *Model) protocolLines(width, count int) []string {
	if len(m.history) == 0 {
		return []string{theme.Muted.Render("Noch keine Meldungen.")}
	}
	entries := m.history
	if len(entries) > count {
		entries = entries[len(entries)-count:]
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		stamp := theme.Muted.Render(entry.at.Format("15:04:05")) + " "
		room := max(1, width-ansi.StringWidth(stamp))
		text := theme.Text.Render(fitLogLine(entry.text, room))
		if dangerText(entry.text) {
			text = theme.Danger.Render(fitLogLine(entry.text, room))
		}
		lines = append(lines, stamp+text)
	}
	return lines
}

// overviewLines describes the library and the settings in effect.
func (m *Model) overviewLines(width int) []string {
	active, newCount := 0, 0
	for _, entry := range m.library {
		if entry.Enabled {
			active++
		}
		newCount += entry.NewCount
	}
	library := fmt.Sprintf("%d %s · %d aktiv", len(m.library), entryWord(len(m.library)), active)
	fresh := theme.Muted.Render("keine")
	if newCount > 0 {
		fresh = theme.Accent.Render(fmt.Sprintf("● %d %s", newCount, episodeWord(newCount)))
	}
	lines := []string{
		overviewRow("Bibliothek", library, width),
		overviewRow("Neue Folgen", fresh, width),
	}
	if m.cfg != nil {
		lines = append(lines, overviewRow("Ziel", m.cfg.DownloadsDir, width))
	}
	lines = append(lines,
		overviewRow("Schema", themeLabel(themeMode), width),
		overviewRow("Version", m.version, width))
	out := lines[:0]
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// overviewRow renders a label and a value in one line; values that do not
// fit are cut. Empty values leave the row out.
func overviewRow(label, value string, width int) string {
	const labelWidth = 13
	if value == "" {
		return ""
	}
	return theme.Muted.Render(padRight(label, labelWidth)) + " " + fitLine(value, max(1, width-labelWidth-1))
}
