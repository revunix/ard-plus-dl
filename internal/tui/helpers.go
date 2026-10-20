// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"fmt"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) readyForDownload() error {
	if err := config.ValidateFileTag(m.cfg.FileTag); err != nil {
		return err
	}
	if m.needsLogin && (m.cfg.Username == "" || m.cfg.Password == "") {
		return fmt.Errorf("Credentials missing! Set ARD_PLUS_USER/ARD_PLUS_PASSWORD or enter them in the form.")
	}
	return nil
}

func (m *Model) rebuildDetailSeasons() {
	m.detailSeasons = nil
	indices := make(map[int]int)
	for _, episode := range m.episodes {
		if episode.Season <= 0 {
			continue
		}
		index, ok := indices[episode.Season]
		if !ok {
			index = len(m.detailSeasons)
			indices[episode.Season] = index
			m.detailSeasons = append(m.detailSeasons, detailSeasonSummary{number: episode.Season})
		}
		season := &m.detailSeasons[index]
		season.total++
		if episode.DownloadedAt == nil {
			season.open++
		}
	}
	sort.Slice(m.detailSeasons, func(i, j int) bool {
		return m.detailSeasons[i].number < m.detailSeasons[j].number
	})
}

func (m *Model) hasDetailSeason(number int) bool {
	for _, season := range m.detailSeasons {
		if season.number == number {
			return true
		}
	}
	return false
}

func (m *Model) openSeasonEpisodes(seasonNumber int) {
	m.activeDetailSeason = seasonNumber
	m.detailSeasonEpisodes = nil
	for _, episode := range m.episodes {
		if episode.Season == seasonNumber {
			m.detailSeasonEpisodes = append(m.detailSeasonEpisodes, episode)
		}
	}
	for index, season := range m.detailSeasons {
		if season.number == seasonNumber {
			m.detailSeasonCursor = index
			break
		}
	}
	m.detailCursor = 0
	m.detailOffset = 0
	m.detailChoice = map[int]bool{}
	m.detailChoiceExplicit = false
	m.detailMessage = ""
	m.screen = ScreenSeasonEpisodes
}

func (m *Model) refreshDetails(entry store.Series) tea.Cmd {
	if m.opts.RefreshEntry == nil || entry.Kind == store.KindMovie {
		return nil
	}
	if m.busy {
		return nil
	}
	m.busy = true
	m.detailMessage = "Folgenliste wird aktualisiert…"
	refresh := m.opts.RefreshEntry
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return detailsRefreshDoneMsg{entryID: entry.ID, err: refresh(ctx, entry)}
	})
}

// libVisible is the number of library rows that fit on the screen.
func (m *Model) libVisible() int {
	if m.height <= 0 {
		return 20
	}
	return max(1, m.height-10)
}

// seriesVisible is the number of season rows that fit on the selection screen.
func (m *Model) seriesVisible() int {
	if m.height <= 0 {
		return 20
	}
	return max(1, m.height-8)
}

// detailVisible is the number of rows that fit in the details lists.
func (m *Model) detailVisible() int {
	if m.height <= 0 {
		return 12
	}
	return max(1, m.height-11)
}

func (m *Model) clampLibOffset() {
	if len(m.library) == 0 {
		m.libCursor = 0
		m.libOffset = 0
		return
	}
	if m.libCursor < 0 {
		m.libCursor = 0
	}
	if m.libCursor >= len(m.library) {
		m.libCursor = len(m.library) - 1
	}
	visible := m.libVisible()
	if m.libCursor < m.libOffset {
		m.libOffset = m.libCursor
	}
	if m.libCursor >= m.libOffset+visible {
		m.libOffset = m.libCursor - visible + 1
	}
	if m.libOffset < 0 {
		m.libOffset = 0
	}
	if limit := len(m.library) - visible; m.libOffset > limit {
		m.libOffset = max(0, limit)
	}
}

func (m *Model) clampSeriesOffset() {
	if len(m.seasons) == 0 {
		m.cursor = 0
		m.seriesOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.seasons) {
		m.cursor = len(m.seasons) - 1
	}
	visible := m.seriesVisible()
	if m.cursor < m.seriesOffset {
		m.seriesOffset = m.cursor
	}
	if m.cursor >= m.seriesOffset+visible {
		m.seriesOffset = m.cursor - visible + 1
	}
	if m.seriesOffset < 0 {
		m.seriesOffset = 0
	}
	if limit := len(m.seasons) - visible; m.seriesOffset > limit {
		m.seriesOffset = max(0, limit)
	}
}

func (m *Model) clampDetailOffset() {
	episodes := m.selectedEpisodeList()
	if len(episodes) == 0 {
		m.detailCursor = 0
		m.detailOffset = 0
		return
	}
	if m.detailCursor < 0 {
		m.detailCursor = 0
	}
	if m.detailCursor >= len(episodes) {
		m.detailCursor = len(episodes) - 1
	}
	visible := m.detailVisible()
	if m.detailCursor < m.detailOffset {
		m.detailOffset = m.detailCursor
	}
	if m.detailCursor >= m.detailOffset+visible {
		m.detailOffset = m.detailCursor - visible + 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	if limit := len(episodes) - visible; m.detailOffset > limit {
		m.detailOffset = max(0, limit)
	}
}

func (m *Model) clampSeasonOffset() {
	// The season cursor is not shown on the season episode list; the offset
	// there belongs to the episode list and is clamped by clampDetailOffset.
	if m.screen == ScreenSeasonEpisodes {
		return
	}
	if len(m.detailSeasons) == 0 {
		m.detailSeasonCursor = 0
		// A flat episode list shares detailOffset and keeps its own position.
		if len(m.episodes) == 0 {
			m.detailOffset = 0
		}
		return
	}
	if m.detailSeasonCursor < 0 {
		m.detailSeasonCursor = 0
	}
	if m.detailSeasonCursor >= len(m.detailSeasons) {
		m.detailSeasonCursor = len(m.detailSeasons) - 1
	}
	visible := m.detailVisible()
	if m.detailSeasonCursor < m.detailOffset {
		m.detailOffset = m.detailSeasonCursor
	}
	if m.detailSeasonCursor >= m.detailOffset+visible {
		m.detailOffset = m.detailSeasonCursor - visible + 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	if limit := len(m.detailSeasons) - visible; m.detailOffset > limit {
		m.detailOffset = max(0, limit)
	}
}

// resizeFields keeps the input widths within the terminal.
func (m *Model) resizeFields() {
	if len(m.fields) != 4 {
		return
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	base := width - 20
	if base < 10 {
		base = 10
	}
	m.fields[0].Width = min(base, 100)
	m.fields[1].Width = min(base, 60)
	m.fields[2].Width = min(base, 60)
	m.fields[3].Width = max(8, min(base, 40))
}

func (m *Model) selectedEpisodeList() []store.KnownEpisode {
	if m.screen == ScreenSeasonEpisodes {
		return m.detailSeasonEpisodes
	}
	return m.episodes
}

// selectedEpisodes returns the chosen IDs, or every open episode in the
// current list when nothing was explicitly selected.
func (m *Model) selectedEpisodes() []string {
	episodes := m.selectedEpisodeList()
	var ids []string
	for index, episode := range episodes {
		if m.detailChoice[index] {
			ids = append(ids, episode.ID)
		}
	}
	if m.detailChoiceExplicit {
		return ids
	}
	if len(ids) > 0 {
		return ids
	}
	for _, episode := range episodes {
		if episode.DownloadedAt == nil {
			ids = append(ids, episode.ID)
		}
	}
	return ids
}

func (m *Model) downloadSeason(seasonNumber int) tea.Cmd {
	var ids []string
	for _, episode := range m.episodes {
		if episode.Season == seasonNumber && episode.DownloadedAt == nil {
			ids = append(ids, episode.ID)
		}
	}
	return m.downloadEpisodeIDs(ids, fmt.Sprintf("Keine offenen Folgen in Staffel %d.", seasonNumber))
}

func (m *Model) downloadDetails() tea.Cmd {
	ids := m.selectedEpisodes()
	if len(ids) == 0 && m.detailChoiceExplicit {
		m.detailMessage = "Keine Folge ausgewählt."
		return nil
	}
	return m.downloadEpisodeIDs(ids, "Keine offenen Folgen.")
}

func (m *Model) downloadEpisodeIDs(ids []string, emptyMessage string) tea.Cmd {
	if m.opts.DownloadEpisodes == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	if len(ids) == 0 {
		m.detailMessage = emptyMessage
		return nil
	}
	if err := m.readyForDownload(); err != nil {
		m.fields[3].SetValue(m.cfg.FileTag)
		m.focusField(3)
		m.startErr = err
		m.screen = ScreenStart
		return nil
	}
	m.busy = true
	m.beginDownload()
	m.detailMessage = fmt.Sprintf("Lade %d Folge(n)…", len(ids))
	download := m.opts.DownloadEpisodes
	entry := m.details
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return downloadNewDoneMsg{entry: entry, err: download(ctx, entry, ids)}
	})
}
