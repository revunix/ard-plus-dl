package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/store"
)

// Messages reported by the asynchronous store commands.

type pendingCountMsg struct {
	entry store.Series
	count int
	err   error
}

type storeActionMsg struct {
	err     error
	message string
}

type detailsLoadedMsg struct {
	entryID  int64
	episodes []store.KnownEpisode
	err      error
}

// loadLibraryCmd reloads the library in the background.
func (m *Model) loadLibraryCmd() tea.Cmd {
	if m.store == nil {
		return nil
	}
	state := m.store
	return m.track(func() tea.Msg {
		entries, err := state.SeriesList()
		return libraryMsg{entries: entries, err: err}
	})
}

func (m *Model) handleLibraryMsg(typed libraryMsg) (tea.Model, tea.Cmd) {
	if typed.err != nil {
		m.libMessage = "Bibliothek: " + typed.err.Error()
		return m, nil
	}
	m.library = typed.entries
	m.clampLibOffset()
	return m, nil
}

// pendingCountCmd asks the store how many episodes are still open.
func (m *Model) pendingCountCmd(entry store.Series) tea.Cmd {
	if m.store == nil {
		return func() tea.Msg {
			return pendingCountMsg{entry: entry, count: entry.NewCount}
		}
	}
	state := m.store
	return m.track(func() tea.Msg {
		count, err := state.CountPending(entry.ID)
		return pendingCountMsg{entry: entry, count: count, err: err}
	})
}

func (m *Model) handlePendingCountMsg(typed pendingCountMsg) (tea.Model, tea.Cmd) {
	if typed.err != nil {
		m.libMessage = typed.err.Error()
		return m, nil
	}
	pending := typed.count
	if pending == 0 && typed.entry.NewCount > 0 {
		pending = typed.entry.NewCount
	}
	if pending == 0 && typed.entry.Kind != store.KindMovie {
		m.libMessage = entryTitle(typed.entry) + ": keine offenen Folgen."
		return m, nil
	}
	if m.busy {
		m.libMessage = "Bitte warten, eine Aktion läuft noch."
		return m, nil
	}
	if err := m.readyForDownload(); err != nil {
		m.fields[3].SetValue(m.cfg.FileTag)
		m.focusField(3)
		m.startErr = err
		pendingEntry := typed.entry
		m.pendingDownload = &pendingEntry
		m.formPurpose = formStart
		m.fields[0].SetValue(typed.entry.URL)
		m.screen = ScreenStart
		return m, nil
	}
	return m, m.downloadNew(typed.entry)
}

// setEnabledCmd stores the enabled flag of a library entry.
func (m *Model) setEnabledCmd(entry store.Series, enabled bool) tea.Cmd {
	if m.store == nil {
		return nil
	}
	state := m.store
	return m.track(func() tea.Msg {
		err := state.SetSeriesEnabled(entry.ID, enabled)
		message := ""
		if err == nil {
			message = entryTitle(entry) + ": aktualisiert."
		}
		return storeActionMsg{err: err, message: message}
	})
}

// removeSeriesCmd deletes a library entry and its episode history.
func (m *Model) removeSeriesCmd(entry store.Series) tea.Cmd {
	if m.store == nil {
		return nil
	}
	state := m.store
	return m.track(func() tea.Msg {
		err := state.RemoveSeries(entry.ID)
		message := ""
		if err == nil {
			message = entryTitle(entry) + " entfernt."
		}
		return storeActionMsg{err: err, message: message}
	})
}

func (m *Model) handleStoreActionMsg(typed storeActionMsg) (tea.Model, tea.Cmd) {
	if typed.err != nil {
		m.libMessage = typed.err.Error()
		return m, nil
	}
	m.libMessage = typed.message
	m.pendingRemoval = 0
	return m, m.loadLibraryCmd()
}

// openDetails switches to the details screen and loads the stored episodes.
func (m *Model) openDetails(entry store.Series) tea.Cmd {
	m.details = entry
	m.episodes = nil
	m.detailSeasons = nil
	m.detailSeasonEpisodes = nil
	m.detailSeasonCursor = 0
	m.activeDetailSeason = 0
	m.detailCursor = 0
	m.detailOffset = 0
	m.detailChoice = map[int]bool{}
	m.detailChoiceExplicit = false
	m.detailReturnSeason = 0
	m.detailMessage = "Folgenliste wird geladen…"
	m.screen = ScreenDetails
	m.rebuildDetailSeasons()
	if m.store == nil {
		m.detailMessage = ""
		return nil
	}
	state := m.store
	return m.track(func() tea.Msg {
		episodes, err := state.Episodes(entry.ID)
		return detailsLoadedMsg{entryID: entry.ID, episodes: episodes, err: err}
	})
}

func (m *Model) handleDetailsLoadedMsg(typed detailsLoadedMsg) (tea.Model, tea.Cmd) {
	if typed.entryID != m.details.ID {
		return m, nil
	}
	if typed.err != nil {
		m.detailMessage = typed.err.Error()
	} else {
		m.episodes = typed.episodes
		m.rebuildDetailSeasons()
		if m.detailMessage == "Folgenliste wird geladen…" {
			m.detailMessage = ""
		}
	}
	m.detailCursor = 0
	m.detailOffset = 0
	m.clampDetailOffset()
	m.clampSeasonOffset()
	if m.detailReturnSeason > 0 {
		season := m.detailReturnSeason
		m.detailReturnSeason = 0
		if m.hasDetailSeason(season) {
			m.openSeasonEpisodes(season)
		} else {
			m.detailMessage = fmt.Sprintf("Staffel %d ist nicht mehr verfügbar.", season)
		}
	}
	return m, nil
}
