package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// The catalog lists every series and every playable movie of ARD Plus, so a
// title can be opened without looking up its address on the website first.

// catalogMsg carries the loaded catalog.
type catalogMsg struct {
	items []ardplus.CatalogItem
	err   error
}

// catalogGenreLimit is the number of genres the filter cycles through.
const catalogGenreLimit = 16

// loadCatalogCmd loads the catalog in the background.
func (m *Model) loadCatalogCmd() tea.Cmd {
	load := m.opts.Catalog
	if load == nil || m.catalogLoading {
		return nil
	}
	m.catalogLoading = true
	m.catalogErr = ""
	ctx := m.rootCtx
	return m.track(func() tea.Msg {
		items, err := load(ctx)
		return catalogMsg{items: items, err: err}
	})
}

// handleCatalogMsg takes the loaded catalog over.
func (m *Model) handleCatalogMsg(msg catalogMsg) (tea.Model, tea.Cmd) {
	m.catalogLoading = false
	if msg.err != nil {
		m.catalogErr = msg.err.Error()
		m.remember("Katalog konnte nicht geladen werden: " + msg.err.Error())
		return m, nil
	}
	m.catalog = msg.items
	m.catalogGenres = topGenres(msg.items, catalogGenreLimit)
	series := 0
	for _, item := range msg.items {
		if item.Kind == ardplus.CatalogSeries {
			series++
		}
	}
	m.remember(fmt.Sprintf("Katalog geladen: %d Serien, %d Filme", series, len(msg.items)-series))
	m.clampCatalogOffset()
	return m, nil
}

// openCatalog switches to the catalog and loads it the first time.
func (m *Model) openCatalog() tea.Cmd {
	m.screen = ScreenCatalog
	m.searching = false
	m.search.Blur()
	m.clampCatalogOffset()
	if m.catalog == nil {
		return m.loadCatalogCmd()
	}
	return nil
}

// topGenres returns the most frequent categories of the catalog. The
// buckets of the Tatort archive by years are left out, the cities stay.
func topGenres(items []ardplus.CatalogItem, limit int) []string {
	counts := map[string]int{}
	for _, item := range items {
		for _, genre := range item.Genres {
			if strings.Contains(genre, ":") {
				continue
			}
			counts[genre]++
		}
	}
	genres := make([]string, 0, len(counts))
	for genre := range counts {
		genres = append(genres, genre)
	}
	sort.Slice(genres, func(i, j int) bool {
		if counts[genres[i]] != counts[genres[j]] {
			return counts[genres[i]] > counts[genres[j]]
		}
		return genres[i] < genres[j]
	})
	if len(genres) > limit {
		genres = genres[:limit]
	}
	return genres
}

// catalogTab splits the catalog into series and movies.
type catalogTab int

// The tabs of the catalog in their order.
const (
	catalogTabSeries catalogTab = iota
	catalogTabMovies
	catalogTabAll
	catalogTabCount
)

// label names a tab.
func (t catalogTab) label() string {
	switch t {
	case catalogTabSeries:
		return "Serien"
	case catalogTabMovies:
		return "Filme"
	default:
		return "Alle"
	}
}

// holds reports whether a title belongs to the tab.
func (t catalogTab) holds(item ardplus.CatalogItem) bool {
	switch t {
	case catalogTabSeries:
		return item.Kind == ardplus.CatalogSeries
	case catalogTabMovies:
		return item.Kind == ardplus.CatalogMovie
	default:
		return true
	}
}

// visibleCatalog returns the titles of the open tab that match the search
// term and the genre filter.
func (m *Model) visibleCatalog() []ardplus.CatalogItem {
	return m.catalogMatches(m.catalogTab)
}

// catalogMatches returns the titles of a tab that match the search term and
// the genre filter.
func (m *Model) catalogMatches(tab catalogTab) []ardplus.CatalogItem {
	term := strings.ToLower(strings.TrimSpace(m.catalogSearch.Value()))
	visible := make([]ardplus.CatalogItem, 0, len(m.catalog))
	for _, item := range m.catalog {
		if !tab.holds(item) {
			continue
		}
		if m.catalogGenre != "" && !hasGenre(item, m.catalogGenre) {
			continue
		}
		if term != "" && !matchesTerm(item, term) {
			continue
		}
		visible = append(visible, item)
	}
	return visible
}

// matchesTerm reports whether every word of the search term stands in the
// title or the genres of a title, so "köln tatort" finds the same as
// "tatort köln".
func matchesTerm(item ardplus.CatalogItem, term string) bool {
	haystack := strings.ToLower(item.Title + " " + strings.Join(item.Genres, " "))
	for _, word := range strings.Fields(term) {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

// switchCatalogTab opens another tab and puts the cursor at its top.
func (m *Model) switchCatalogTab(tab catalogTab) {
	m.catalogTab = (tab + catalogTabCount) % catalogTabCount
	m.catalogCursor = 0
	m.catalogOffset = 0
	m.clampCatalogOffset()
}

// hasGenre reports whether a title belongs to a category.
func hasGenre(item ardplus.CatalogItem, genre string) bool {
	for _, candidate := range item.Genres {
		if candidate == genre {
			return true
		}
	}
	return false
}

// catalogItem returns the title under the cursor.
func (m *Model) catalogItem() (ardplus.CatalogItem, bool) {
	visible := m.visibleCatalog()
	if m.catalogCursor < 0 || m.catalogCursor >= len(visible) {
		return ardplus.CatalogItem{}, false
	}
	return visible[m.catalogCursor], true
}

// libraryEntryFor returns the library entry of a catalog title. Entries are
// matched by the content id, so an address written by hand matches too.
func (m *Model) libraryEntryFor(item ardplus.CatalogItem) (store.Series, bool) {
	for _, entry := range m.library {
		if ardplus.DetailID(entry.URL) == item.ID {
			return entry, true
		}
	}
	return store.Series{}, false
}

// catalogKind returns the library kind of a catalog title.
func catalogKind(item ardplus.CatalogItem) string {
	if item.Kind == ardplus.CatalogSeries {
		return store.KindSeries
	}
	return store.KindMovie
}

// adoptCatalogItem puts a catalog title into the library, if it is not
// there yet, and returns its entry.
func (m *Model) adoptCatalogItem(item ardplus.CatalogItem) (store.Series, bool, error) {
	if entry, ok := m.libraryEntryFor(item); ok {
		return entry, false, nil
	}
	if m.store == nil {
		return store.Series{}, false, fmt.Errorf("keine Bibliothek geöffnet")
	}
	if _, err := m.store.UpsertSeries(item.URL(), item.Title, catalogKind(item)); err != nil {
		return store.Series{}, false, err
	}
	m.loadLibrary()
	entry, ok := m.libraryEntryFor(item)
	if !ok {
		return store.Series{}, false, fmt.Errorf("%s fehlt nach dem Speichern in der Bibliothek", item.Title)
	}
	return entry, true, nil
}

// handleCatalogKey handles the keys of the catalog.
func (m *Model) handleCatalogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.catalogSearching {
		return m.handleCatalogSearchKey(msg)
	}
	if m.busy {
		switch key {
		case "enter", "o", "a":
			m.setToast("Bitte warten, eine Aktion läuft noch.", toastInfo)
			return m, nil
		}
	}
	switch key {
	case "q":
		return m.quit(0)
	case "tab", "b":
		m.screen = ScreenLibrary
		m.clampLibOffset()
		return m, nil
	case "esc":
		if m.catalogSearch.Value() != "" || m.catalogGenre != "" {
			m.catalogSearch.SetValue("")
			m.catalogGenre = ""
			m.clampCatalogOffset()
			return m, nil
		}
		// The catalog is the home screen: esc has nowhere to go.
		return m, nil
	case "s":
		m.openSettings()
		return m, nil
	case "/":
		m.catalogSearching = true
		m.catalogSearch.Focus()
		return m, textinput.Blink
	case "left", "h":
		m.switchCatalogTab(m.catalogTab - 1)
		return m, nil
	case "right", "l":
		m.switchCatalogTab(m.catalogTab + 1)
		return m, nil
	case "1", "2", "3":
		m.switchCatalogTab(catalogTab(key[0] - '1'))
		return m, nil
	case "g", "G":
		m.catalogGenre = cycleGenre(m.catalogGenres, m.catalogGenre, key == "G")
		m.catalogCursor = 0
		m.catalogOffset = 0
		m.clampCatalogOffset()
		return m, nil
	case "r":
		return m, m.loadCatalogCmd()
	case "up", "k":
		m.catalogCursor--
	case "down", "j":
		m.catalogCursor++
	case "pgup":
		m.catalogCursor -= max(1, m.catalogVisibleRows()-1)
	case "pgdown":
		m.catalogCursor += max(1, m.catalogVisibleRows()-1)
	case "home":
		m.catalogCursor = 0
	case "end":
		m.catalogCursor = len(m.visibleCatalog()) - 1
	case "y":
		if item, ok := m.catalogItem(); ok {
			m.copyAction(item.URL())
		}
		return m, nil
	case "a":
		item, ok := m.catalogItem()
		if !ok {
			return m, nil
		}
		_, added, err := m.adoptCatalogItem(item)
		switch {
		case err != nil:
			m.setToast("Bibliothek: "+err.Error(), toastError)
		case added:
			m.setToast(item.Title+" ist jetzt in der Bibliothek.", toastSuccess)
		default:
			m.setToast(item.Title+" ist schon in der Bibliothek.", toastInfo)
		}
		return m, nil
	case "enter", "o":
		item, ok := m.catalogItem()
		if !ok {
			return m, nil
		}
		entry, added, err := m.adoptCatalogItem(item)
		if err != nil {
			m.setToast("Bibliothek: "+err.Error(), toastError)
			return m, nil
		}
		if added {
			m.remember(item.Title + " in die Bibliothek aufgenommen")
		}
		cmd := m.openDetails(entry)
		m.detailsFrom = ScreenCatalog
		return m, tea.Batch(cmd, m.refreshDetails(entry))
	default:
		return m, nil
	}
	m.clampCatalogOffset()
	return m, nil
}

// handleCatalogSearchKey edits the search term of the catalog.
func (m *Model) handleCatalogSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.catalogSearching = false
		m.catalogSearch.Blur()
		return m, nil
	case "esc":
		m.catalogSearching = false
		m.catalogSearch.Blur()
		m.catalogSearch.SetValue("")
		m.clampCatalogOffset()
		return m, nil
	case "up":
		m.catalogCursor--
		m.clampCatalogOffset()
		return m, nil
	case "down":
		m.catalogCursor++
		m.clampCatalogOffset()
		return m, nil
	}
	updated, cmd := m.catalogSearch.Update(msg)
	m.catalogSearch = updated
	m.catalogCursor = 0
	m.catalogOffset = 0
	m.clampCatalogOffset()
	return m, cmd
}

// cycleGenre returns the next genre of the filter; after the last one the
// filter shows everything again.
func cycleGenre(genres []string, current string, backwards bool) string {
	if len(genres) == 0 {
		return ""
	}
	index := -1
	for position, genre := range genres {
		if genre == current {
			index = position
		}
	}
	if backwards {
		switch {
		case index < 0:
			return genres[len(genres)-1]
		case index == 0:
			return ""
		}
		return genres[index-1]
	}
	if index+1 >= len(genres) {
		return ""
	}
	return genres[index+1]
}

// catalogVisibleRows returns the number of titles the list shows.
func (m *Model) catalogVisibleRows() int {
	if m.height <= 0 {
		return 20
	}
	// The tabs and the search take two lines above the column heading.
	rows := m.height - 5 - catalogTopRows - m.headerGap() - m.lowerRows() - m.footerHeight(m.frameWidth(), m.hints().short)
	return max(1, rows)
}

// clampCatalogOffset keeps the cursor inside the list and on the screen.
func (m *Model) clampCatalogOffset() {
	total := len(m.visibleCatalog())
	if total == 0 {
		m.catalogCursor, m.catalogOffset = 0, 0
		return
	}
	m.catalogCursor = min(max(m.catalogCursor, 0), total-1)
	rows := m.catalogVisibleRows()
	if m.catalogCursor < m.catalogOffset {
		m.catalogOffset = m.catalogCursor
	}
	if m.catalogCursor >= m.catalogOffset+rows {
		m.catalogOffset = m.catalogCursor - rows + 1
	}
	m.catalogOffset = min(max(m.catalogOffset, 0), max(0, total-rows))
}

// catalogView shows the catalog: the titles on the left, the title under the
// cursor on the right, the protocol and the overview below.
func (m *Model) catalogView() []string {
	width := m.frameWidth()
	header := m.header("Katalog", m.catalogSubtitle())
	footer := m.screenFooter(width)
	footer = append(m.lowerBoxes(width), footer...)

	extra := 1 + catalogTopRows // the tabs, the search and the column heading
	budget := listBudget(m.height, header, footer, extra, width)
	if m.height <= 0 {
		budget = len(m.visibleCatalog())
	}
	budget = min(budget, m.catalogVisibleRows())
	fill := m.boxHeight(header, footer, 0)

	meta := ""
	if m.catalogGenre != "" {
		meta = m.catalogGenre
	}
	title := "Katalog"
	leftWidth, rightWidth := m.splitAt(width, 0.55)
	if rightWidth <= 0 {
		return m.frameRows(header, renderCard(width, title, padLines(m.catalogRows(cardInner(width), budget), fill), true, meta), footer)
	}
	left := renderCard(leftWidth, title, padLines(m.catalogRows(cardInner(leftWidth), budget), fill), true, meta)
	right := renderCard(rightWidth, "Angaben", padLines(clampLines(m.catalogDetail(cardInner(rightWidth)), max(fill, budget+extra)), fill), false, "")
	return m.frameRows(header, splitColumns(left, right, leftWidth, rightWidth), footer)
}

// catalogSubtitle summarizes the catalog for the head.
func (m *Model) catalogSubtitle() string {
	if m.catalog == nil {
		return ""
	}
	series := 0
	for _, item := range m.catalog {
		if item.Kind == ardplus.CatalogSeries {
			series++
		}
	}
	return fmt.Sprintf("%d Serien · %d Filme", series, len(m.catalog)-series)
}

// catalogRows renders the list of the titles.
func (m *Model) catalogRows(inner, budget int) []string {
	rows := []string{m.catalogTabs(inner), m.catalogSearchRow(inner)}
	switch {
	case m.catalog == nil && m.catalogErr != "":
		return append(rows, clampLines(renderEmptyAction(inner, "Katalog nicht erreichbar.", m.catalogErr, "r", "erneut laden"), budget)...)
	case m.catalog == nil:
		return append(rows, renderSkeleton(inner, min(max(budget, 1), 8))...)
	}
	visible := m.visibleCatalog()
	if len(visible) == 0 {
		return append(rows, clampLines(renderEmptyAction(inner, "Nichts gefunden.", "Ändere den Suchbegriff oder das Genre.", "esc", "Filter löschen"), budget)...)
	}

	// The middle column fits the tab: the seasons of a series, the length
	// of a movie, the kind when both stand in the list.
	yearWidth, kindWidth := 4, 8
	titleWidth := inner - 4 - 1 - kindWidth - 1 - yearWidth
	rows = append(rows, theme.Muted.Render(fitLine("    "+padRight("TITEL", titleWidth)+" "+padRight(m.catalogColumn(), kindWidth)+" JAHR", inner)))
	start, end, top, bottom := windowRange(len(visible), m.catalogCursor, m.catalogOffset, budget)
	if top {
		rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
	}
	for index := start; index < end; index++ {
		item := visible[index]
		active := index == m.catalogCursor
		state := "  "
		if _, ok := m.libraryEntryFor(item); ok {
			state = theme.Accent.Render("● ")
		}
		title := padRight(item.Title, titleWidth)
		if active {
			title = theme.Selected.Render(title)
		}
		kind := theme.Muted.Render(padRight(m.catalogCell(item), kindWidth))
		year := ""
		if item.Year > 0 {
			year = fmt.Sprintf("%d", item.Year)
		}
		line := fitLine(cursorMark(active)+state+title+" "+kind+" "+theme.Muted.Render(year), inner)
		if active {
			line = highlightRow(line, inner)
		}
		rows = append(rows, line)
	}
	if bottom {
		rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(visible)-end)))
	}
	return rows
}

// catalogTopRows is the number of lines above the column heading: the tabs
// and the search.
const catalogTopRows = 2

// catalogTabs renders the tabs with the number of matching titles. The open
// tab stands out in the brand color on the background of a marked line.
func (m *Model) catalogTabs(width int) string {
	parts := make([]string, 0, catalogTabCount)
	for tab := catalogTab(0); tab < catalogTabCount; tab++ {
		label := fmt.Sprintf(" %s %d ", tab.label(), len(m.catalogMatches(tab)))
		if tab == m.catalogTab {
			parts = append(parts, onSurface(theme.Accent.Bold(true).Render(label), theme.Row))
			continue
		}
		parts = append(parts, theme.Muted.Render(label))
	}
	hint := theme.Subtle.Render("  ← →")
	return fitLine(strings.Join(parts, " ")+hint, width)
}

// catalogSearchRow renders the search field. It always stands above the
// list, so the search is easy to find; / puts the cursor into it.
func (m *Model) catalogSearchRow(width int) string {
	label := theme.Muted.Render("Suche  ")
	if m.catalogSearching || m.catalogSearch.Value() != "" {
		return fitLine(cursorMark(m.catalogSearching)+label+m.catalogSearch.View(), width)
	}
	return fitLine("  "+label+theme.Subtle.Render("/ drücken und Titel oder Genre tippen"), width)
}

// catalogColumn names the middle column of the open tab.
func (m *Model) catalogColumn() string {
	switch m.catalogTab {
	case catalogTabSeries:
		return "STAFFELN"
	case catalogTabMovies:
		return "LÄNGE"
	default:
		return "TYP"
	}
}

// catalogCell fills the middle column for a title.
func (m *Model) catalogCell(item ardplus.CatalogItem) string {
	switch m.catalogTab {
	case catalogTabSeries:
		if item.Seasons > 0 {
			return fmt.Sprintf("%d", item.Seasons)
		}
		return ""
	case catalogTabMovies:
		if item.Runtime > 0 {
			minutes := (item.Runtime + 30) / 60
			return fmt.Sprintf("%d:%02d h", minutes/60, minutes%60)
		}
		return ""
	default:
		return catalogKindLabel(item)
	}
}

// catalogKindLabel names the kind of a title.
func catalogKindLabel(item ardplus.CatalogItem) string {
	if item.Kind == ardplus.CatalogSeries {
		return "Serie"
	}
	return "Film"
}

// catalogDetail describes the title under the cursor.
func (m *Model) catalogDetail(width int) []string {
	item, ok := m.catalogItem()
	if !ok {
		if m.catalogLoading {
			return []string{m.spinnerView() + " " + theme.Muted.Render("Katalog wird geladen…")}
		}
		return nil
	}
	lines := []string{}
	for _, line := range wrapText(item.Title, m.flowWidth(width)) {
		lines = append(lines, theme.Title.Render(line))
	}
	facts := []string{catalogKindLabel(item)}
	if item.Kind == ardplus.CatalogSeries && item.Seasons > 0 {
		facts = append(facts, fmt.Sprintf("%d %s", item.Seasons, seasonWord(item.Seasons)))
	}
	if item.Runtime > 0 {
		facts = append(facts, runtimeLabel(item.Runtime))
	}
	if item.Year > 0 {
		facts = append(facts, fmt.Sprintf("%d", item.Year))
	}
	lines = append(lines, theme.Muted.Render(strings.Join(facts, " · ")))
	if genres := displayGenres(item.Genres, 4); genres != "" {
		lines = append(lines, theme.Accent.Render(fitLine(genres, width)))
	}
	lines = append(lines, "")
	if entry, ok := m.libraryEntryFor(item); ok {
		state := "● in der Bibliothek"
		if entry.NewCount > 0 {
			state += fmt.Sprintf(" · %d neu", entry.NewCount)
		}
		lines = append(lines, theme.Success.Render(state))
	} else {
		lines = append(lines, theme.Muted.Render("○ noch nicht in der Bibliothek"))
	}
	lines = append(lines, "")
	if item.Synopsis != "" {
		lines = append(lines, wrapText(item.Synopsis, m.flowWidth(width))...)
		lines = append(lines, "")
	}
	lines = append(lines, renderKeycap("enter")+" "+theme.KeyLabel.Render("öffnen")+theme.Subtle.Render("  ·  ")+
		renderKeycap("a")+" "+theme.KeyLabel.Render("aufnehmen"))
	for index, line := range lines {
		lines[index] = fitLine(line, width)
	}
	return lines
}

// displayGenres joins the first categories of a title, without the year
// buckets of the Tatort archive.
func displayGenres(genres []string, limit int) string {
	shown := []string{}
	for _, genre := range genres {
		if strings.Contains(genre, ":") {
			continue
		}
		shown = append(shown, genre)
		if len(shown) == limit {
			break
		}
	}
	return strings.Join(shown, " · ")
}

// runtimeLabel renders a running time.
func runtimeLabel(seconds int) string {
	minutes := (seconds + 30) / 60
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d h %02d min", minutes/60, minutes%60)
}

// seasonWord names a number of seasons.
func seasonWord(count int) string {
	if count == 1 {
		return "Staffel"
	}
	return "Staffeln"
}
