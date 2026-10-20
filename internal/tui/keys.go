// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		switch m.screen {
		case ScreenQueue, ScreenLoading:
			if m.cancelled {
				return m.quit(130)
			}
			m.cancelled = true
			m.cancel()
			return m, nil
		default:
			return m.quit(130)
		}
	}

	if key == "?" {
		m.helpVisible = !m.helpVisible
		return m, nil
	}
	if m.helpVisible {
		m.helpVisible = false
		return m, nil
	}

	switch m.screen {
	case ScreenLibrary:
		return m.handleLibraryKey(key)

	case ScreenDetails:
		return m.handleDetailsKey(key)

	case ScreenSeasonEpisodes:
		return m.handleSeasonEpisodesKey(key)
	case ScreenStart:
		switch key {
		case "tab", "down":
			m.focusField((m.focus + 1) % len(m.fields))
			return m, nil
		case "shift+tab", "up":
			m.focusField((m.focus + len(m.fields) - 1) % len(m.fields))
			return m, nil
		case "esc":
			m.pendingDownload = nil
			m.formPurpose = formStart
			if len(m.library) > 0 {
				m.screen = ScreenLibrary
				return m, nil
			}
		case "enter":
			if m.busy {
				m.startErr = errors.New("Eine Aktion läuft noch. Bitte warten.")
				return m, nil
			}
			if err := m.applyStartInput(); err != nil {
				m.startErr = err
				return m, nil
			}
			m.startErr = nil
			m.persistStart()
			if m.pendingDownload != nil {
				entry := *m.pendingDownload
				m.pendingDownload = nil
				return m, m.downloadNew(entry)
			}
			if m.formPurpose == formSettings || (m.cfg.URL == "" && len(m.library) > 0) {
				// Settings-only edit: nothing to download.
				m.loadLibrary()
				m.screen = ScreenLibrary
				m.libMessage = "Gespeichert."
				return m, nil
			}
			return m, m.startRun()
		}
		updated, cmd := m.fields[m.focus].Update(msg)
		m.fields[m.focus] = updated
		return m, cmd

	case ScreenSeries:
		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.clampSeriesOffset()
		case "down", "j":
			if m.cursor < len(m.seasons)-1 {
				m.cursor++
			}
			m.clampSeriesOffset()
		case "pgup":
			m.cursor -= max(1, m.seriesVisible()-1)
			m.clampSeriesOffset()
		case "pgdown":
			m.cursor += max(1, m.seriesVisible()-1)
			m.clampSeriesOffset()
		case "home":
			m.cursor = 0
			m.clampSeriesOffset()
		case "end":
			m.cursor = len(m.seasons) - 1
			m.clampSeriesOffset()
		case " ":
			m.toggleSeason(m.cursor)
		case "a":
			allSelected := true
			for _, season := range m.seasons {
				if !m.selection[season.Number] {
					allSelected = false
					break
				}
			}
			for _, season := range m.seasons {
				m.selection[season.Number] = !allSelected
			}
			m.selectionErr = ""
		case "enter":
			if m.categoryMode {
				slugs := m.selectedCategories()
				if len(slugs) == 0 {
					// The screen stays open: the user can still pick a
					// category and start the run.
					m.selectionErr = "Bitte mindestens eine Kategorie wählen."
					return m, nil
				}
				if m.categoriesReply != nil {
					reply := m.categoriesReply
					m.categoriesReply = nil
					reply <- slugs
				}
				m.categoriesDone = nil
				m.selectionErr = ""
				m.screen = ScreenLoading
				return m, nil
			}
			selected := m.selectedSeasons()
			if len(selected) == 0 {
				m.selectionErr = "Bitte mindestens eine Staffel wählen."
				return m, nil
			}
			if m.seasonsReply != nil {
				reply := m.seasonsReply
				m.seasonsReply = nil
				reply <- selected
			}
			m.seasonsDone = nil
			m.selectionErr = ""
			m.screen = ScreenLoading
			return m, nil
		}
		return m, nil

	case ScreenTatort:
		if key == "enter" {
			count, err := strconv.Atoi(strings.TrimSpace(m.skipField.Value()))
			if err != nil || count < 0 || count > m.tatortCount {
				m.skipErr = fmt.Sprintf("Bitte eine Zahl zwischen 0 und %d eingeben.", m.tatortCount)
				return m, nil
			}
			if m.skipReply != nil {
				reply := m.skipReply
				m.skipReply = nil
				reply <- count
			}
			m.skipDone = nil
			m.skipErr = ""
			m.screen = ScreenLoading
			return m, nil
		}
		m.skipErr = ""
		updated, cmd := m.skipField.Update(msg)
		m.skipField = updated
		return m, cmd

	case ScreenQueue, ScreenLoading:
		if key == "q" {
			if m.cancelled {
				return m.quit(130)
			}
			m.cancelled = true
			m.cancel()
		}
		return m, nil

	case ScreenSummary:
		switch key {
		case "l":
			m.loadLibrary()
			m.screen = ScreenLibrary
			return m, nil
		case "q", "enter":
			return m.quit(0)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) handleLibraryKey(key string) (tea.Model, tea.Cmd) {
	if m.busy {
		switch key {
		case "n", "c", "d", "enter", "o", " ", "x":
			m.libMessage = "Bitte warten, eine Aktion läuft noch."
			return m, nil
		}
	}
	if key != "x" {
		m.pendingRemoval = 0
	}
	switch key {
	case "q":
		return m.quit(0)
	case "up", "k":
		if m.libCursor > 0 {
			m.libCursor--
		}
		m.clampLibOffset()
	case "down", "j":
		if m.libCursor < len(m.library)-1 {
			m.libCursor++
		}
		m.clampLibOffset()
	case "pgup":
		m.libCursor -= max(1, m.libVisible()-1)
		m.clampLibOffset()
	case "pgdown":
		m.libCursor += max(1, m.libVisible()-1)
		m.clampLibOffset()
	case "home":
		m.libCursor = 0
		m.clampLibOffset()
	case "end":
		m.libCursor = len(m.library) - 1
		m.clampLibOffset()
	case "n", "c":
		return m, m.checkLibrary()
	case "s":
		m.formPurpose = formSettings
		m.pendingDownload = nil
		m.fields[0].SetValue(m.cfg.URL)
		m.fields[3].SetValue(m.cfg.FileTag)
		m.focusField(3)
		m.startErr = nil
		m.screen = ScreenStart
		return m, nil
	case "a":
		m.formPurpose = formAddURL
		m.pendingDownload = nil
		m.fields[0].SetValue("")
		m.focusField(0)
		m.startErr = nil
		m.screen = ScreenStart
		return m, nil
	case "enter", "o":
		if len(m.library) == 0 {
			return m, nil
		}
		entry := m.library[m.libCursor]
		cmd := m.openDetails(entry)
		return m, tea.Batch(cmd, m.refreshDetails(entry))
	case "d":
		if len(m.library) == 0 {
			return m, nil
		}
		return m, m.pendingCountCmd(m.library[m.libCursor])
	case " ":
		if len(m.library) == 0 || m.store == nil {
			return m, nil
		}
		entry := m.library[m.libCursor]
		return m, m.setEnabledCmd(entry, !entry.Enabled)
	case "x":
		if len(m.library) == 0 || m.store == nil {
			return m, nil
		}
		entry := m.library[m.libCursor]
		if m.pendingRemoval != entry.ID {
			m.pendingRemoval = entry.ID
			m.libMessage = entryTitle(entry) + ": x erneut drücken zum Entfernen."
			return m, nil
		}
		return m, m.removeSeriesCmd(entry)
	}
	return m, nil
}

// readyForDownload reports whether the settings needed for a download are
// complete; otherwise the user is sent to the form.

func (m *Model) handleDetailsKey(key string) (tea.Model, tea.Cmd) {
	if m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0 {
		switch key {
		case "q", "esc", "backspace":
			m.screen = ScreenLibrary
			return m, nil
		case "up", "k":
			if m.detailSeasonCursor > 0 {
				m.detailSeasonCursor--
			}
			m.clampSeasonOffset()
		case "down", "j":
			if m.detailSeasonCursor < len(m.detailSeasons)-1 {
				m.detailSeasonCursor++
			}
			m.clampSeasonOffset()
		case "pgup":
			m.detailSeasonCursor -= max(1, m.detailVisible()-1)
			m.clampSeasonOffset()
		case "pgdown":
			m.detailSeasonCursor += max(1, m.detailVisible()-1)
			m.clampSeasonOffset()
		case "home":
			m.detailSeasonCursor = 0
			m.clampSeasonOffset()
		case "end":
			m.detailSeasonCursor = len(m.detailSeasons) - 1
			m.clampSeasonOffset()
		case "enter", "o":
			season := m.detailSeasons[m.detailSeasonCursor].number
			m.openSeasonEpisodes(season)
			return m, nil
		case "d":
			season := m.detailSeasons[m.detailSeasonCursor].number
			return m, m.downloadSeason(season)
		}
		return m, nil
	}
	return m.handleEpisodeListKey(key, false)
}

func (m *Model) handleSeasonEpisodesKey(key string) (tea.Model, tea.Cmd) {
	return m.handleEpisodeListKey(key, true)
}

func (m *Model) handleEpisodeListKey(key string, inSeason bool) (tea.Model, tea.Cmd) {
	episodes := m.episodes
	if inSeason {
		episodes = m.detailSeasonEpisodes
	}
	switch key {
	case "q":
		m.screen = ScreenLibrary
		return m, nil
	case "esc", "backspace":
		if inSeason {
			m.screen = ScreenDetails
			m.detailSeasonEpisodes = nil
			m.detailChoice = map[int]bool{}
			m.detailChoiceExplicit = false
			m.detailMessage = ""
			m.clampSeasonOffset()
		} else {
			m.screen = ScreenLibrary
		}
		return m, nil
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
		m.clampDetailOffset()
	case "down", "j":
		if m.detailCursor < len(episodes)-1 {
			m.detailCursor++
		}
		m.clampDetailOffset()
	case "pgup":
		m.detailCursor -= max(1, m.detailVisible()-1)
		m.clampDetailOffset()
	case "pgdown":
		m.detailCursor += max(1, m.detailVisible()-1)
		m.clampDetailOffset()
	case "home":
		m.detailCursor = 0
		m.clampDetailOffset()
	case "end":
		m.detailCursor = len(episodes) - 1
		m.clampDetailOffset()
	case " ":
		if m.detailCursor < len(episodes) {
			m.detailChoice[m.detailCursor] = !m.detailChoice[m.detailCursor]
			m.detailChoiceExplicit = true
		}
	case "a":
		selected := 0
		for _, chosen := range m.detailChoice {
			if chosen {
				selected++
			}
		}
		all := selected == len(episodes)
		m.detailChoice = map[int]bool{}
		if !all {
			for index := range episodes {
				m.detailChoice[index] = true
			}
		}
		m.detailChoiceExplicit = true
	case "d", "enter":
		return m, m.downloadDetails()
	}
	return m, nil
}
