package tui

import (
	"fmt"
	"path/filepath"
	"strings"

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
		m.setToast("Bibliothek: "+typed.err.Error(), toastError)
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
		m.setToast(typed.err.Error(), toastError)
		return m, nil
	}
	pending := typed.count
	if pending == 0 && typed.entry.NewCount > 0 {
		pending = typed.entry.NewCount
	}
	if pending == 0 && typed.entry.Kind != store.KindMovie {
		m.setToast(entryTitle(typed.entry)+": keine offenen Folgen.", toastInfo)
		return m, nil
	}
	if m.busy {
		m.setToast("Bitte warten, eine Aktion läuft noch.", toastInfo)
		return m, nil
	}
	if err := m.readyForDownload(); err != nil {
		m.focusField(1)
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

// editEntryCmd stores the skip count of a library entry.
func (m *Model) editEntryCmd(skip int) tea.Cmd {
	if m.store == nil {
		return nil
	}
	state := m.store
	entry := m.editEntry
	return m.track(func() tea.Msg {
		err := state.SetSeriesSkip(entry.ID, skip)
		message := ""
		if err == nil {
			message = entryTitle(entry) + ": gespeichert."
		}
		return storeActionMsg{err: err, message: message}
	})
}

// removeJobFileCmd deletes the file of the active download and forgets that
// the episode was downloaded.
func (m *Model) removeJobFileCmd() tea.Cmd {
	index := m.currentJobIndex()
	if index < 0 || index >= len(m.jobs) {
		return nil
	}
	job := m.jobs[index]
	if job.path == "" {
		m.setToast("Für diese Datei gibt es keinen Pfad.", toastError)
		return nil
	}
	if err := m.checkDownloadPath(job.path); err != nil {
		m.setToast("Datei nicht gelöscht: "+err.Error(), toastError)
		return nil
	}
	if !m.pendingRemovalFile {
		m.pendingRemovalFile = true
		m.libMessage = "x erneut drücken, um die Datei zu löschen: " + job.path
		return nil
	}
	m.pendingRemovalFile = false
	if err := m.removeDownloadFile(job.path); err != nil {
		m.setToast("Datei nicht gelöscht: "+err.Error(), toastError)
		return nil
	}
	// The episode counts as open again.
	if m.store != nil && job.seriesID > 0 && job.episodeID != "" {
		if err := m.store.ClearDownloaded(job.seriesID, job.episodeID); err != nil {
			m.setToast("Datei gelöscht, Verlauf nicht zurückgesetzt: "+err.Error(), toastError)
			return nil
		}
	}
	m.jobs[index].status = "gelöscht"
	m.setToast("Datei gelöscht: "+filepath.Base(job.path), toastSuccess)
	return m.loadLibraryCmd()
}

// resetEntryCmd deletes the downloaded files of a series and forgets its
// history, so every episode is offered again.
func (m *Model) resetEntryCmd(entry store.Series) tea.Cmd {
	if m.store == nil {
		return nil
	}
	state := m.store
	return m.track(func() tea.Msg {
		episodes, err := state.Episodes(entry.ID)
		if err != nil {
			return storeActionMsg{err: err}
		}
		removed := 0
		for _, episode := range episodes {
			if episode.Path == "" {
				continue
			}
			if err := m.removeDownloadFile(episode.Path); err != nil {
				continue
			}
			removed++
		}
		if err := state.ClearEpisodes(entry.ID); err != nil {
			return storeActionMsg{err: err}
		}
		message := fmt.Sprintf("%s zurückgesetzt: %d Datei(en) gelöscht, Verlauf geleert.",
			entryTitle(entry), removed)
		return storeActionMsg{message: message}
	})
}

// removeMarkedCmd deletes the marked entries of the library.
func (m *Model) removeMarkedCmd(entries []store.Series) tea.Cmd {
	if m.store == nil || len(entries) == 0 {
		return nil
	}
	state := m.store
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entryTitle(entry))
	}
	m.libSelected = map[int64]bool{}
	return m.track(func() tea.Msg {
		var failure error
		for _, entry := range entries {
			if err := state.RemoveSeries(entry.ID); err != nil {
				failure = err
				break
			}
		}
		message := fmt.Sprintf("%d %s entfernt.", len(names), entryWord(len(names)))
		if failure != nil {
			message = strings.Join(names, ", ") + ": " + failure.Error()
		}
		return storeActionMsg{err: failure, message: message}
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
		m.setToast(typed.err.Error(), toastError)
		return m, nil
	}
	m.setToast(typed.message, toastSuccess)
	m.pendingRemoval = 0
	if m.screen == ScreenEditEntry {
		// The settings are saved, the library takes over again.
		m.screen = ScreenLibrary
	}
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
	m.detailsFrom = ScreenLibrary
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
			m.setToast(fmt.Sprintf("Staffel %d ist nicht mehr verfügbar.", season), toastError)
		}
	}
	return m, nil
}
