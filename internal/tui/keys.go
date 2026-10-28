// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// A new key press dismisses the toast of the last action.
	m.clearToast()
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

	if m.confirm != nil {
		// The window takes every key until it is answered.
		return m.handleConfirmKey(msg)
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
	case ScreenSplash:
		if key == "q" {
			return m.quit(0)
		}
		return m, nil

	case ScreenLibrary:
		return m.handleLibraryKey(msg)

	case ScreenCatalog:
		return m.handleCatalogKey(msg)

	case ScreenDetails:
		return m.handleDetailsKey(key)

	case ScreenSeasonEpisodes:
		return m.handleSeasonEpisodesKey(key)

	case ScreenEditEntry:
		return m.handleEditEntryKey(msg)

	case ScreenStart:
		switch key {
		case "tab", "down":
			m.focusField(m.nextField(1))
			return m, nil
		case "shift+tab", "up":
			m.focusField(m.nextField(-1))
			return m, nil
		case "esc":
			m.pendingDownload = nil
			m.formPurpose = formStart
			if m.hasCatalog() || len(m.library) > 0 {
				m.screen = m.homeScreen()
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
			if m.formPurpose == formSettings || (m.cfg.URL == "" && (m.hasCatalog() || len(m.library) > 0)) {
				// Settings-only edit: nothing to download.
				m.loadLibrary()
				m.screen = m.homeScreen()
				m.setToast("Gespeichert.", toastSuccess)
				return m, nil
			}
			return m, m.startRun()
		}
		if m.focus == m.themeFieldIndex() {
			// The color scheme is not a text field, so the keys change it.
			switch key {
			case "h", "d", "t", "left", "right", "up", "down":
				m.changeTheme(key)
				return m, nil
			}
		}
		if m.focus >= len(m.fields) {
			return m, nil
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
		if key == "l" {
			m.logVisible = !m.logVisible
			return m, nil
		}
		if key == "y" {
			m.copyAction(m.currentJobPath())
			return m, nil
		}
		if key == "o" {
			if err := revealPath(m.currentJobPath()); err != nil {
				m.setToast("Ordner nicht geöffnet: "+err.Error(), toastError)
				return m, nil
			}
			m.setToast("Ordner geöffnet.", toastSuccess)
			return m, nil
		}
		if key == "x" {
			return m, m.removeJobFileCmd()
		}
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

// handleEditEntryKey handles the settings of a single series.
func (m *Model) handleEditEntryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.screen = ScreenLibrary
		m.editErr = ""
		return m, nil
	case "enter":
		if m.busy {
			m.editErr = "Eine Aktion läuft noch. Bitte warten."
			return m, nil
		}
		skip, err := m.editSkipValue()
		if err != nil {
			m.editErr = err.Error()
			return m, nil
		}
		m.editErr = ""
		return m, m.editEntryCmd(skip)
	}
	updated, cmd := m.editSkip.Update(msg)
	m.editSkip = updated
	return m, cmd
}

// editSkipValue reads the number of episodes to skip. Zero and one skip
// nothing, the same rule the library uses.
func (m *Model) editSkipValue() (int, error) {
	raw := strings.TrimSpace(m.editSkip.Value())
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("Bitte eine Zahl ab 0 eingeben.")
	}
	return value, nil
}

func (m *Model) handleLibraryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.busy {
		switch key {
		case "n", "c", "d", "enter", "o", " ", "x":
			m.setToast("Bitte warten, eine Aktion läuft noch.", toastInfo)
			return m, nil
		}
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	switch key {
	case "q":
		return m.quit(0)
	case "tab":
		return m, m.openCatalog()
	case "/":
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink
	case "up", "k":
		if m.libCursor > 0 {
			m.libCursor--
		}
		m.clampLibOffset()
	case "down", "j":
		if m.libCursor < len(m.visibleLibrary())-1 {
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
		m.libCursor = len(m.visibleLibrary()) - 1
		m.clampLibOffset()
	case "n", "c":
		return m, m.checkLibrary()
	case "s":
		m.openSettings()
		return m, nil
	case "e":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		m.editEntry = entry
		skip := ""
		if entry.Skip > 0 {
			skip = strconv.Itoa(entry.Skip)
		}
		m.editSkip.SetValue(skip)
		m.editSkip.Focus()
		m.editErr = ""
		m.screen = ScreenEditEntry
		return m, nil
	case "y":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		m.copyAction(entry.URL)
		return m, nil
	case "m":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		if m.libSelected == nil {
			m.libSelected = map[int64]bool{}
		}
		m.libSelected[entry.ID] = !m.libSelected[entry.ID]
		if !m.libSelected[entry.ID] {
			delete(m.libSelected, entry.ID)
		}
		m.libMessage = m.selectionMessage()
		return m, nil
	case "a":
		if m.hasCatalog() {
			// Titles come from the catalog; the address is not typed by hand.
			return m, m.openCatalog()
		}
		m.formPurpose = formAddURL
		m.pendingDownload = nil
		m.fields[0].SetValue("")
		m.focusField(0)
		m.startErr = nil
		m.screen = ScreenStart
		return m, nil
	case "esc":
		if m.filterActive() {
			m.search.SetValue("")
			m.clampLibOffset()
			return m, nil
		}
		if m.hasCatalog() {
			return m, m.openCatalog()
		}
		return m, nil
	case "enter", "o":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		m.searching = false
		m.search.Blur()
		cmd := m.openDetails(entry)
		return m, tea.Batch(cmd, m.refreshDetails(entry))
	case "d":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		return m, m.pendingCountCmd(entry)
	case " ":
		entry, ok := m.libraryEntry()
		if !ok || m.store == nil {
			return m, nil
		}
		return m, m.setEnabledCmd(entry, !entry.Enabled)
	case "r":
		entry, ok := m.libraryEntry()
		if !ok {
			return m, nil
		}
		m.askResetEntry(entry)
		return m, nil
	case "x":
		entry, ok := m.libraryEntry()
		if !ok || m.store == nil {
			return m, nil
		}
		if marked := m.markedEntries(); len(marked) > 0 {
			// The marked entries are removed together.
			m.askRemoveMarked(marked)
			return m, nil
		}
		m.askRemoveEntry(entry)
		return m, nil
	}
	return m, nil
}

// markedEntries returns the marked entries of the library in the order of the
// list.
func (m *Model) markedEntries() []store.Series {
	if len(m.libSelected) == 0 {
		return nil
	}
	marked := make([]store.Series, 0, len(m.libSelected))
	for _, entry := range m.visibleLibrary() {
		if m.libSelected[entry.ID] {
			marked = append(marked, entry)
		}
	}
	return marked
}

// selectionMessage describes the marked entries.
func (m *Model) selectionMessage() string {
	marked := m.markedEntries()
	if len(marked) == 0 {
		return "Auswahl aufgehoben."
	}
	return fmt.Sprintf("%d %s markiert.", len(marked), entryWord(len(marked)))
}

// handleSearchKey handles the search field of the library.
func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	case "esc":
		m.searching = false
		m.search.Blur()
		m.search.SetValue("")
		m.clampLibOffset()
		return m, nil
	case "up", "down":
		// The cursor moves, the search term stays where it is.
	default:
		updated, cmd := m.search.Update(msg)
		m.search = updated
		m.clampLibOffset()
		return m, cmd
	}
	switch key {
	case "up", "k":
		if m.libCursor > 0 {
			m.libCursor--
		}
		m.clampLibOffset()
	case "down", "j":
		if m.libCursor < len(m.visibleLibrary())-1 {
			m.libCursor++
		}
		m.clampLibOffset()
	}
	return m, nil
}

// readyForDownload reports whether the settings needed for a download are
// complete; otherwise the user is sent to the form.

func (m *Model) handleDetailsKey(key string) (tea.Model, tea.Cmd) {
	if m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0 {
		switch key {
		case "q", "esc", "backspace":
			m.screen = m.detailsBack()
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
		case "y":
			m.copyAction(m.details.URL)
			return m, nil
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
			m.screen = m.detailsBack()
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
	case "y":
		if m.detailCursor < len(episodes) {
			m.copyAction(episodes[m.detailCursor].Title)
		}
		return m, nil
	}
	return m, nil
}

// detailsBack returns the screen the details were opened from: the library
// or the catalog.
func (m *Model) detailsBack() Screen {
	if m.detailsFrom == ScreenCatalog {
		return ScreenCatalog
	}
	return ScreenLibrary
}

// hasCatalog reports whether the catalog of ARD Plus is available. It is
// the home of the interface then.
func (m *Model) hasCatalog() bool {
	return m.opts.Catalog != nil
}

// homeScreen returns the screen the interface comes back to: the catalog,
// or the library when there is no catalog.
func (m *Model) homeScreen() Screen {
	if m.hasCatalog() {
		return ScreenCatalog
	}
	return ScreenLibrary
}

// hidesURLField reports whether the form leaves the address out. With the
// catalog the titles are chosen there, not typed in.
func (m *Model) hidesURLField() bool {
	return m.hasCatalog() && m.formPurpose != formAddURL
}

// nextField returns the entry of the form that follows the focused one in
// the given direction; the hidden address field is skipped.
func (m *Model) nextField(step int) int {
	count := m.fieldCount()
	next := m.focus
	for range count {
		next = (next + step + count) % count
		if next == 0 && m.hidesURLField() {
			continue
		}
		return next
	}
	return m.focus
}

// openSettings opens the form with the account and the color scheme.
func (m *Model) openSettings() {
	m.formPurpose = formSettings
	m.pendingDownload = nil
	m.fields[0].SetValue(m.cfg.URL)
	m.focusField(1)
	m.startErr = nil
	m.screen = ScreenStart
}
