// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) readyForDownload() error {
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
	m.detailMessage = "Folgenliste wird aktualisiert…"
	refresh := m.opts.RefreshEntry
	ctx := m.ctx
	m.setBusy(true)
	return m.track(func() tea.Msg {
		return detailsRefreshDoneMsg{entryID: entry.ID, err: refresh(ctx, entry)}
	})
}

// splashDuration is how long the splash screen stays visible.
const splashDuration = 1100 * time.Millisecond

// splashDoneMsg hands over from the splash screen to the app.
type splashDoneMsg struct{}

// splashTick plans the end of the splash screen.
func (m *Model) splashTick() tea.Cmd {
	return tea.Tick(splashDuration, func(time.Time) tea.Msg {
		return splashDoneMsg{}
	})
}

// themeFieldIndex is the position of the color scheme in the form.
func (m *Model) themeFieldIndex() int {
	return len(m.fields)
}

// showsThemeField reports whether the form offers the color scheme.
func (m *Model) showsThemeField() bool {
	return m.formPurpose == formSettings
}

// fieldCount is the number of focusable entries of the form.
func (m *Model) fieldCount() int {
	count := len(m.fields)
	if m.showsThemeField() {
		count++
	}
	return max(1, count)
}

// changeTheme switches the color scheme with the given key.
func (m *Model) changeTheme(key string) {
	switch key {
	case "h":
		m.setTheme(ThemeLight)
	case "d":
		m.setTheme(ThemeDark)
	case "t":
		m.setTheme(ThemeAuto)
	case "left", "up":
		m.setTheme(nextTheme(themeMode))
	case "right", "down":
		m.setTheme(previousTheme(themeMode))
	}
}

// loadTheme reads the stored color scheme and applies it.
func (m *Model) loadTheme() {
	if m.store == nil {
		return
	}
	value, err := m.store.Setting(store.SettingTheme)
	if err != nil {
		return
	}
	applyTheme(parseTheme(value))
}

// setTheme switches the color scheme and remembers the choice.
func (m *Model) setTheme(mode ThemeMode) {
	applyTheme(mode)
	if m.store == nil {
		return
	}
	if err := m.store.SetSetting(store.SettingTheme, themeValue(mode)); err != nil {
		m.setToast("Erscheinungsbild konnte nicht gespeichert werden: "+err.Error(), toastError)
	}
}

// clockInterval is the delay between two frames of the loading animation.
const clockInterval = 250 * time.Millisecond

// clockMsg advances the clock of the loading animation.
type clockMsg struct{}

// clockTick plans the next frame of the loading animation.
func (m *Model) clockTick() tea.Cmd {
	return tea.Tick(clockInterval, func(time.Time) tea.Msg {
		return clockMsg{}
	})
}

// setBusy marks the model as busy and starts or stops the animation.
func (m *Model) setBusy(busy bool) {
	m.busy = busy
}

// statusText describes the running action for the top bar.
func (m *Model) statusText() string {
	if !m.busy {
		return ""
	}
	if m.libMessage != "" {
		return m.libMessage
	}
	if strings.HasSuffix(m.detailMessage, "…") {
		return m.detailMessage
	}
	return "Lade…"
}

// spinnerView renders the loading animation.
func (m *Model) spinnerView() string {
	if !m.busy {
		return ""
	}
	return m.spinner.View()
}

// checkDownloadPath makes sure that a file lies inside the downloads
// directory. Only such files may be deleted.
func (m *Model) checkDownloadPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("kein Dateipfad")
	}
	dir := strings.TrimSpace(m.cfg.DownloadsDir)
	if dir == "" {
		return errors.New("kein Download-Ordner bekannt")
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Pfad liegt nicht im Download-Ordner: %s", path)
	}
	return nil
}

// removeDownloadFile deletes a downloaded file and forgets that the episode
// was downloaded, so it is offered again.
func (m *Model) removeDownloadFile(path string) error {
	if err := m.checkDownloadPath(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// revealPath opens the folder of a file in the file manager of the system.
func revealPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("kein Zielpfad")
	}
	folder := filepath.Dir(path)
	if _, err := os.Stat(folder); err != nil {
		return fmt.Errorf("Ordner %s nicht gefunden", folder)
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", folder)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", folder)
	default:
		command = exec.Command("xdg-open", folder)
	}
	return command.Start()
}

// copyToClipboard puts a text into the clipboard of the system.
func copyToClipboard(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("nichts zu kopieren")
	}
	return clipboard.WriteAll(text)
}

// copyAction copies a text and reports the result as a toast.
func (m *Model) copyAction(text string) {
	if err := copyToClipboard(text); err != nil {
		m.setToast("Kopieren nicht möglich: "+err.Error(), toastError)
		return
	}
	m.setToast("Kopiert: "+truncate(text, 60), toastSuccess)
}

// estimateRemaining estimates the time a download still needs. The progress
// is counted in segments, so the result is a rough estimate; it is only
// returned when at least two progress messages arrived.
func estimateRemaining(done, total int, elapsed time.Duration) (time.Duration, bool) {
	if done <= 0 || total <= done || elapsed <= 0 {
		return 0, false
	}
	return remainingFromRate(done, total, elapsed/time.Duration(done))
}

// remainingFromRate estimates the remaining time from the time a single
// segment needs.
func remainingFromRate(done, total int, perSegment time.Duration) (time.Duration, bool) {
	if done <= 0 || total <= done || perSegment <= 0 {
		return 0, false
	}
	return perSegment * time.Duration(total-done), true
}

// remainingLabel renders the estimated remaining time.
func remainingLabel(remaining time.Duration) string {
	if remaining <= 0 {
		return ""
	}
	if remaining < time.Minute {
		return fmt.Sprintf("noch ~%d s", int(remaining.Seconds()))
	}
	return fmt.Sprintf("noch ~%d min", int(remaining.Minutes()))
}

// setToast shows a short message above the key hints. It stays until the
// user continues.
func (m *Model) setToast(message string, level toastLevel) {
	m.toast = message
	m.toastLevel = level
	m.remember(message)
}

// clearToast removes the toast right away.
func (m *Model) clearToast() {
	m.toast = ""
}

// toastStyle returns the style of the current toast.
func (m *Model) toastStyle() lipgloss.Style {
	switch m.toastLevel {
	case toastSuccess:
		return theme.PillDone
	case toastError:
		return theme.PillError
	default:
		return theme.PillNeutral
	}
}

// footerWithToast renders the key hints with an optional toast above them.
// The footer only shows the most important keys of the screen; the help popup
// lists all of them.
func (m *Model) footerWithToast(width int, hints []keyHint) []string {
	lines := renderFooter(width, hints)
	if m.toast == "" {
		return lines
	}
	return append([]string{m.toastLine(width)}, lines...)
}

// screenFooter renders the footer of the current screen.
func (m *Model) screenFooter(width int) []string {
	hints := m.hints().short
	hints = append(hints, keyHint{Key: "?", Label: "Hilfe"})
	return m.footerWithToast(width, hints)
}

// toastLine renders the toast right aligned above the key hints.
func (m *Model) toastLine(width int) string {
	text := m.toast
	room := width - 4
	if room < 4 {
		return ""
	}
	if len(text) > room {
		text = text[:room-1] + "…"
	}
	// The chip adds one cell of padding on each side.
	padding := max(0, width-ansi.StringWidth(text)-2)
	return strings.Repeat(" ", padding) + m.toastStyle().Render(text)
}

// footerHeight is the number of rows the footer occupies.
func (m *Model) footerHeight(width int, hints []keyHint) int {
	height := len(renderFooter(width, hints))
	if m.toast != "" {
		height++
	}
	return height
}

// header renders the top bar with the title, the context and the status of
// the running action.
func (m *Model) header(title, subtitle string) []string {
	status := ""
	if m.busy {
		status = m.spinnerView() + " " + m.statusText()
	}
	lines := renderHeader(m.frameWidth(), title, subtitle, status)
	if m.headerGap() > 0 {
		// A free line between the head and the boxes, like in btop.
		lines = append(lines, "")
	}
	return lines
}

// headerGap returns the free lines below the head.
func (m *Model) headerGap() int {
	if m.height <= 0 || m.height >= 16 {
		return 1
	}
	return 0
}

// searchTerm returns the term the library is filtered by.
func (m *Model) searchTerm() string {
	return strings.TrimSpace(m.search.Value())
}

// filterActive reports whether the library is filtered.
func (m *Model) filterActive() bool {
	return m.searchTerm() != ""
}

// visibleLibrary returns the entries the library shows. Without a search term
// that is the whole library.
func (m *Model) visibleLibrary() []store.Series {
	term := m.searchTerm()
	if term == "" {
		return m.library
	}
	visible := make([]store.Series, 0, len(m.library))
	for _, entry := range m.library {
		if strings.Contains(strings.ToLower(entryTitle(entry)), strings.ToLower(term)) ||
			strings.Contains(strings.ToLower(entry.URL), strings.ToLower(term)) {
			visible = append(visible, entry)
		}
	}
	return visible
}

// libraryEntry returns the entry under the cursor.
func (m *Model) libraryEntry() (store.Series, bool) {
	visible := m.visibleLibrary()
	if len(visible) == 0 {
		return store.Series{}, false
	}
	return visible[min(m.libCursor, len(visible)-1)], true
}

// libVisible is the number of library rows that fit on the screen.
func (m *Model) libVisible() int {
	if m.height <= 0 {
		return 20
	}
	// Header, card, column heading and footer take the rest.
	return max(1, m.height-5-m.headerGap()-m.lowerRows()-m.footerHeight(m.frameWidth(), m.hints().short))
}

// seriesVisible is the number of season rows that fit on the selection screen.
func (m *Model) seriesVisible() int {
	if m.height <= 0 {
		return 20
	}
	return max(1, m.height-4-m.footerHeight(m.frameWidth(), m.hints().short))
}

// detailVisible is the number of rows that fit in the details lists.
func (m *Model) detailVisible() int {
	if m.height <= 0 {
		return 12
	}
	footer := m.footerHeight(m.frameWidth(), m.hints().short)
	extra := 0
	if m.frameWidth() < minSplitWidth {
		// The narrow layout shows the counts above the card.
		extra = 1
	}
	return max(1, m.height-4-m.headerGap()-m.lowerRows()-footer-extra)
}

func (m *Model) clampLibOffset() {
	visible := m.visibleLibrary()
	if len(visible) == 0 {
		m.libCursor = 0
		m.libOffset = 0
		return
	}
	if m.libCursor < 0 {
		m.libCursor = 0
	}
	if m.libCursor >= len(visible) {
		m.libCursor = len(visible) - 1
	}
	rows := m.libVisible()
	if m.libCursor < m.libOffset {
		m.libOffset = m.libCursor
	}
	if m.libCursor >= m.libOffset+rows {
		m.libOffset = m.libCursor - rows + 1
	}
	if m.libOffset < 0 {
		m.libOffset = 0
	}
	if limit := len(visible) - rows; m.libOffset > limit {
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
	if len(m.fields) != 3 {
		return
	}
	width := m.contentWidth()
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
		m.focusField(1)
		m.startErr = err
		m.screen = ScreenStart
		return nil
	}
	m.beginDownload()
	m.detailMessage = fmt.Sprintf("Lade %d Folge(n)…", len(ids))
	download := m.opts.DownloadEpisodes
	entry := m.details
	ctx := m.ctx
	m.setBusy(true)
	return m.track(func() tea.Msg {
		return downloadNewDoneMsg{entry: entry, err: download(ctx, entry, ids)}
	})
}
