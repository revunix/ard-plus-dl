package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/store"
)

// confirmKind is the action a confirmation window asks for.
type confirmKind int

// The actions the library asks about before it runs them.
const (
	confirmRemoveEntry confirmKind = iota
	confirmRemoveMarked
	confirmResetEntry
)

// confirmPrompt is an open confirmation window. The action waits until the
// user confirms it, so a key that deletes something never acts on the first
// press alone.
type confirmPrompt struct {
	kind   confirmKind
	entry  store.Series
	marked []store.Series
}

// askRemoveEntry opens the window that asks before an entry leaves the
// library.
func (m *Model) askRemoveEntry(entry store.Series) {
	m.confirm = &confirmPrompt{kind: confirmRemoveEntry, entry: entry}
	m.libMessage = ""
}

// askRemoveMarked opens the window that asks before the marked entries leave
// the library.
func (m *Model) askRemoveMarked(entries []store.Series) {
	if len(entries) == 0 {
		return
	}
	m.confirm = &confirmPrompt{kind: confirmRemoveMarked, marked: entries}
	m.libMessage = ""
}

// askResetEntry opens the window that asks before the files and the history
// of a series are deleted.
func (m *Model) askResetEntry(entry store.Series) {
	m.confirm = &confirmPrompt{kind: confirmResetEntry, entry: entry}
	m.libMessage = ""
}

// handleConfirmKey answers the open window: enter or y runs the action, esc
// or n closes the window without changing anything. Every other key is
// ignored, so the page below cannot be edited while the window is open.
func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		prompt := m.confirm
		m.confirm = nil
		if prompt == nil {
			return m, nil
		}
		switch prompt.kind {
		case confirmRemoveEntry:
			return m, m.removeSeriesCmd(prompt.entry)
		case confirmRemoveMarked:
			return m, m.removeMarkedCmd(prompt.marked)
		case confirmResetEntry:
			return m, m.resetEntryCmd(prompt.entry)
		}
	case "esc", "n", "q":
		m.confirm = nil
	}
	return m, nil
}

// confirmView draws the open window over the page.
func (m *Model) confirmView() []string {
	prompt := m.confirm
	if prompt == nil {
		return nil
	}
	width := 58
	if frame := m.frameWidth(); frame > 0 {
		width = min(width, frame)
	}
	inner := max(20, width-2*windowPadding)
	text := func(value string) []string {
		return wrapText(theme.Text.Render(value), inner)
	}

	var title string
	var lines []string
	verb := "entfernen"
	switch prompt.kind {
	case confirmRemoveEntry:
		title = "Eintrag entfernen"
		lines = append(lines, theme.Accent.Bold(true).Render(fitLine(entryTitle(prompt.entry), inner)), "")
		lines = append(lines, text("Der Eintrag verschwindet aus der Bibliothek. Bereits geladene Dateien bleiben auf der Platte.")...)
	case confirmRemoveMarked:
		title = fmt.Sprintf("%d %s entfernen", len(prompt.marked), entryWord(len(prompt.marked)))
		// A short terminal still has to show the question and the keys: the
		// list of names gives way first.
		shown := 5
		if m.height > 0 {
			shown = min(shown, max(1, m.height-10))
		}
		for index, entry := range prompt.marked {
			if index == shown {
				lines = append(lines, theme.Muted.Render(fmt.Sprintf("… und %d weitere", len(prompt.marked)-shown)))
				break
			}
			lines = append(lines, theme.Accent.Render(fitLine(entryTitle(entry), inner)))
		}
		lines = append(lines, "")
		lines = append(lines, text("Die Einträge verschwinden aus der Bibliothek. Bereits geladene Dateien bleiben auf der Platte.")...)
	case confirmResetEntry:
		title = "Serie zurücksetzen"
		verb = "löschen"
		lines = append(lines, theme.Accent.Bold(true).Render(fitLine(entryTitle(prompt.entry), inner)), "")
		lines = append(lines, text("Die geladenen Dateien dieser Serie werden gelöscht und der Verlauf geleert. Danach gelten alle Folgen wieder als offen.")...)
	}

	lines = append(lines, "")
	lines = append(lines, renderKeycap("enter")+" "+theme.Danger.Render(verb)+
		"    "+renderKeycap("esc")+" "+theme.Muted.Render("abbrechen"))
	return renderWindow(width, title, "", lines)
}
