package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) queueView() []string {
	width := m.frameWidth()
	jobIndex := m.currentJobIndex()
	position := 0
	if jobIndex >= 0 {
		position = jobIndex + 1
	}
	header := m.header("Downloads", fmt.Sprintf("Datei %d von %d", position, len(m.jobs)))
	footer := m.screenFooter(width)

	maxContent := 1 << 30
	if m.height > 0 {
		reserved := len(header) + len(footer)
		if m.height >= 18 {
			// The blank line between the cards; the head brings its own.
			reserved++
		}
		if m.cancelled {
			reserved += 2
		}
		maxContent = max(0, m.height-reserved)
	}

	spacious := m.height <= 0 || m.height >= 18
	var contentLines []string
	queue := m.queueCard(width, maxContent)
	contentLines = append(contentLines, queue...)
	remaining := maxContent - len(queue)
	if remaining > 0 {
		if spacious && remaining > 1 && m.logVisible {
			// The reserved rows already hold this line.
			contentLines = append(contentLines, "")
		}
		if m.logVisible {
			contentLines = append(contentLines, m.activityPanel(width, remaining)...)
		} else {
			contentLines = append(contentLines, fitLine(theme.Muted.Render("l Aktivität einblenden"), width))
		}
	}

	if m.cancelled {
		contentLines = append(contentLines, "", fitLine(theme.Danger.Render("Abbruch angefordert…"), width))
	}
	return m.frameRows(header, contentLines, footer)
}

// logToggleLabel describes what the l key does next.
func (m *Model) logToggleLabel() string {
	if m.logVisible {
		return "Aktivität aus"
	}
	return "Aktivität an"
}

// queueCard renders the list of all download jobs.
func (m *Model) queueCard(width, maxLines int) []string {
	inner := m.cardTextWidth(width)
	rows := m.queueRows(inner, maxLines-cardOverhead(width))
	return m.renderCardBudget(width, "Queue", rows, maxLines)
}

// queueRows renders one line per job. The active job carries the progress
// bar and the target path.
func (m *Model) queueRows(inner, budget int) []string {
	if len(m.jobs) == 0 {
		return []string{theme.Muted.Render(fitLine("Download wird vorbereitet…", inner))}
	}
	jobIndex := m.currentJobIndex()
	finished, failed := 0, 0
	for _, current := range m.jobs {
		switch current.status {
		case "fertig":
			finished++
		case "fehlgeschlagen":
			failed++
		}
	}

	rows := []string{}
	start, end := 0, len(m.jobs)
	if budget > 0 && len(m.jobs) > budget {
		// Keep the active job inside the visible window.
		start = max(0, jobIndex-budget/2)
		end = min(len(m.jobs), start+budget)
		start = max(0, end-budget)
	}
	if start > 0 {
		rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
	}
	for index := start; index < end; index++ {
		item := m.jobs[index]
		kind := pillNeutral
		switch item.status {
		case "lädt":
			kind = pillOpen
		case "fertig":
			kind = pillDone
		case "fehlgeschlagen":
			kind = pillError
		case "gelöscht":
			kind = pillNeutral
		}
		pill := renderPill(item.status, kind)
		progress := ""
		if item.total > 0 {
			progress = progressLabel(item.done, item.total)
		}
		cursor := "  "
		if index == jobIndex {
			cursor = cursorMark(true)
		}
		// The label gives way to the progress so that both stay readable.
		labelWidth := inner - ansi.StringWidth(cursor) - ansi.StringWidth(pill) - 1 - ansi.StringWidth(progress) - 1
		if progress == "" {
			labelWidth = inner - ansi.StringWidth(cursor) - ansi.StringWidth(pill) - 1
		}
		label := fitToWidth(item.label, max(10, labelWidth))
		if index == jobIndex {
			label = theme.Selected.Render(label)
		}
		head := cursor + pill + " " + label
		if progress != "" {
			gap := inner - ansi.StringWidth(head) - ansi.StringWidth(progress)
			if gap < 1 {
				gap = 1
			}
			head += strings.Repeat(" ", gap) + theme.Muted.Render(progress)
		}
		if index == jobIndex {
			head = highlightRow(head, inner)
		}
		rows = append(rows, fitLine(head, inner))
		if index == jobIndex {
			if item.total > 0 {
				// The bar spans the box below the line of the job.
				barWidth := max(inner-2, 8)
				rows = append(rows, fitLine("  "+progressBar(item.done, item.total, barWidth), inner))
			}
			if remaining := m.jobRemaining(item); remaining != "" {
				rows = append(rows, fitLine("  "+theme.Muted.Render(remaining), inner))
			}
			if item.path != "" {
				rows = append(rows, fitLine("  "+theme.Muted.Render("Ziel")+"  "+item.path, inner))
			}
		}
	}
	if end < len(m.jobs) {
		rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(m.jobs)-end)))
	}
	if finished > 0 || failed > 0 {
		counter := fmt.Sprintf("%d fertig", finished)
		if failed > 0 {
			counter += fmt.Sprintf(" · %d fehlgeschlagen", failed)
		}
		rows = append(rows, fitLine("  "+theme.Muted.Render(counter), inner))
	}
	return rows
}

// renderCardBudget shrinks the content until the card fits the available
// height.
func (m *Model) renderCardBudget(width int, title string, content []string, maxLines int) []string {
	return m.renderCardBudgetFocus(width, title, content, maxLines, true)
}

// renderCardBudgetFocus shrinks the content until the box fits the available
// height; the frame shows whether the keys work in the box.
func (m *Model) renderCardBudgetFocus(width int, title string, content []string, maxLines int, focused bool) []string {
	if maxLines <= 0 {
		maxLines = 1 << 30
	}
	if maxLines < cardOverhead(width) {
		return nil
	}
	for len(content) > 0 {
		panel := renderCard(width, title, content, focused, "")
		if len(panel) <= maxLines {
			return panel
		}
		content = content[:len(content)-1]
	}
	return renderCard(width, title, content, focused, "")
}

func (m *Model) currentJobIndex() int {
	for index := len(m.jobs) - 1; index >= 0; index-- {
		if m.jobs[index].status == "lädt" {
			return index
		}
	}
	for index := len(m.jobs) - 1; index >= 0; index-- {
		if m.jobs[index].status == "wartet" {
			return index
		}
	}
	return len(m.jobs) - 1
}

func (m *Model) activityPanel(width, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	inner := m.cardTextWidth(width)
	overhead := cardOverhead(width)
	count := maxLines - overhead
	if count < 1 {
		count = 1
	}
	logs := m.tailLog(count)
	var content []string
	if len(logs) == 0 {
		if !m.hasCompletedJob() {
			content = append(content, theme.Muted.Render(fitLogLine("Warte auf Download-Meldungen…", inner)))
		}
	}
	for _, line := range logs {
		rendered := "· " + line
		if dangerText(line) {
			rendered = "· " + theme.Danger.Render(line)
		}
		content = append(content, fitLogLine(rendered, inner))
	}
	// The protocol fills the rest of the screen and stands back behind the
	// queue the keys work in.
	if m.height > 0 {
		content = padLines(content, maxLines-overhead)
	}
	return m.renderCardBudgetFocus(width, "Aktivität", content, maxLines, false)
}

func (m *Model) hasCompletedJob() bool {
	for _, item := range m.jobs {
		if item.status == "fertig" || item.status == "fehlgeschlagen" {
			return true
		}
	}
	return false
}

// currentJobPath returns the target path of the active download.
func (m *Model) currentJobPath() string {
	index := m.currentJobIndex()
	if index < 0 || index >= len(m.jobs) {
		return ""
	}
	return m.jobs[index].path
}

// jobRemaining estimates the remaining time of a job.
func (m *Model) jobRemaining(item job) string {
	remaining, ok := remainingFromRate(item.done, item.total, item.perSegment)
	if !ok {
		return ""
	}
	return remainingLabel(remaining)
}

func (m *Model) libraryView() []string {
	width := m.frameWidth()
	subtitle := librarySubtitle(m.library)
	header := m.header("Bibliothek", subtitle)
	footer := m.screenFooter(width)
	footer = append(m.lowerBoxes(width), footer...)

	inner := m.cardTextWidth(width)
	extra := 1 // column heading inside the card
	if m.filterActive() || m.searching {
		extra++ // the search field
	}
	if m.libMessage != "" {
		extra += 2
	}
	budget := listBudget(m.height, header, footer, extra, width)
	if m.height <= 0 {
		budget = len(m.visibleLibrary())
	}
	budget = min(budget, m.libVisible())

	notice := 0
	if m.libMessage != "" && !m.busy {
		notice = 2
	}
	fill := m.boxHeight(header, footer, notice)
	var body []string
	leftWidth, rightWidth := m.splitAt(width, 0.55)
	if rightWidth > 0 {
		// Wide terminals show the list next to the details of the entry
		// under the cursor.
		left := renderCard(leftWidth, "Serien", padLines(m.libraryRows(cardInner(leftWidth), budget, true), fill), true, "")
		right := renderCard(rightWidth, "Details", padLines(clampLines(m.libraryDetail(cardInner(rightWidth)), budget), fill), false, "")
		body = splitColumns(left, right, leftWidth, rightWidth)
	} else {
		body = renderCard(width, "Serien", padLines(m.libraryRows(inner, budget, false), fill), true, "")
	}
	if m.libMessage != "" && !m.busy {
		// While the app works, the status stands in the top bar.
		body = append(body, "", fitLine(renderNotice(m.libMessage), width))
	}
	return m.frameRows(header, body, footer)
}

// libraryRows renders the table of library entries.
func (m *Model) libraryRows(inner, budget int, compact bool) []string {
	visible := m.visibleLibrary()
	rows := []string{}
	if m.filterActive() || m.searching {
		rows = append(rows, m.searchRow(inner))
	}
	if len(visible) == 0 {
		if m.filterActive() {
			return append(rows, clampLines(renderEmptyAction(inner, "Nichts gefunden.", "Ändere den Suchbegriff.", "esc", "Filter löschen"), budget)...)
		}
		return clampLines(renderEmptyAction(inner, "Noch keine Serien gespeichert.", "Im Katalog stehen alle Titel von ARD Plus.", "tab", "Katalog öffnen"), budget)
	}
	rows = append(rows, theme.Muted.Render(fitLine(libraryColumns(inner, compact).header(), inner)))
	if budget > 0 {
		start, end, top, bottom := windowRange(len(visible), m.libCursor, m.libOffset, budget)
		if top {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
		}
		for index := start; index < end; index++ {
			rows = append(rows, m.libraryLine(visible[index], index, inner, compact))
		}
		if bottom {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(visible)-end)))
		}
	}
	return rows
}

// searchRow renders the search field above the table.
func (m *Model) searchRow(width int) string {
	marker := "  "
	if m.searching {
		marker = cursorMark(true)
	}
	return fitLine(marker+theme.Muted.Render("Suche  ")+m.search.View(), width)
}

// libraryDetail describes the entry under the cursor.
func (m *Model) libraryDetail(width int) []string {
	entry, ok := m.libraryEntry()
	if !ok {
		if m.filterActive() {
			return renderEmptyAction(width, "Nichts gefunden.", "Ändere den Suchbegriff.", "esc", "Filter löschen")
		}
		return renderEmptyAction(width, "Keine Auswahl.", "", "a", "URL hinzufügen")
	}
	lines := []string{}
	for _, line := range wrapText(entryTitle(entry), m.flowWidth(width)) {
		lines = append(lines, theme.Title.Render(line))
	}
	state := renderPill("aktiv", pillOpen)
	if !entry.Enabled {
		state = renderPill("pausiert", pillNeutral)
	}
	lines = append(lines, renderBadge(kindLabel(entry.Kind))+" "+state)
	lines = append(lines, "")
	lines = append(lines, detailRows("URL", entry.URL, width)...)
	if entry.NewCount > 0 {
		lines = append(lines, detailRows("Neu", fmt.Sprintf("%d %s", entry.NewCount, episodeWord(entry.NewCount)), width)...)
	} else {
		lines = append(lines, detailRows("Neu", "keine", width)...)
	}
	lines = append(lines, detailRows("Geprüft", checkedLabel(entry.CheckedAt), width)...)
	lines = append(lines, detailRows("Hinzugefügt", addedLabel(entry.AddedAt), width)...)
	lines = append(lines, detailRows("Überspringen", skipLabel(entry.Skip), width)...)
	lines = append(lines, "")
	lines = append(lines, theme.Muted.Render("e Einstellungen der Serie"))
	if entry.LastError != "" {
		for _, line := range wrapText("Letzter Fehler: "+entry.LastError, m.flowWidth(width)) {
			lines = append(lines, theme.Danger.Render(line))
		}
	}
	return lines
}

// checkedLabel renders the moment of the last check.
func checkedLabel(checked *time.Time) string {
	if checked == nil {
		return "noch nie"
	}
	return checked.Local().Format("02.01.2006 15:04")
}

// addedLabel renders the moment the entry was added.
func addedLabel(added time.Time) string {
	if added.IsZero() {
		return "unbekannt"
	}
	return added.Local().Format("02.01.2006")
}

// skipLabel renders the number of episodes the entry skips.
func skipLabel(skip int) string {
	if skip <= 1 {
		return "keine"
	}
	return fmt.Sprintf("%d %s", skip, episodeWord(skip))
}

// detailRows renders a label with a value below it. Long values wrap into
// the column.
func detailRows(label, value string, width int) []string {
	const labelWidth = 13
	head := theme.Muted.Render(padRight(label, labelWidth)) + " "
	wrapped := wrapText(value, max(1, min(width, maxContentWidth)-labelWidth-1))
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, head+line)
			continue
		}
		lines = append(lines, strings.Repeat(" ", labelWidth+1)+line)
	}
	return lines
}

// librarySubtitle summarizes entry counts plus new episodes and disabled entries.
func librarySubtitle(entries []store.Series) string {
	label := fmt.Sprintf("%d Einträge", len(entries))
	if len(entries) == 1 {
		label = "1 Eintrag"
	}
	newCount, disabled := 0, 0
	for _, entry := range entries {
		newCount += entry.NewCount
		if !entry.Enabled {
			disabled++
		}
	}
	if newCount > 0 {
		label += fmt.Sprintf(" · %d neu", newCount)
	}
	if disabled > 0 {
		label += fmt.Sprintf(" · %d aus", disabled)
	}
	return label
}

type libraryCols struct {
	title    int
	kind     int
	newCount int
	date     int
	showKind bool
	showNew  bool
	showDate bool
}

// libraryColumns measures the table. In compact mode the list shows the
// title only, because the details column carries the rest.
func libraryColumns(width int, compact bool) libraryCols {
	cols := libraryCols{showKind: !compact}
	if !compact {
		cols.kind = 8
		cols.showNew = width >= 56
		cols.showDate = width >= 84
	}
	if cols.showNew {
		cols.newCount = 8
	}
	if cols.showDate {
		cols.date = 6
	}
	fixed := 4
	if cols.showKind {
		fixed += 1 + cols.kind
	}
	if cols.showNew {
		fixed += 1 + cols.newCount
	}
	if cols.showDate {
		fixed += 1 + cols.date
	}
	cols.title = max(6, width-fixed)
	return cols
}

func (c libraryCols) header() string {
	line := "    " + padRight("TITEL", c.title)
	if c.showKind {
		line += " " + padRight("TYP", c.kind)
	}
	if c.showNew {
		line += " " + padRight("NEU", c.newCount)
	}
	if c.showDate {
		line += " " + padRight("DATUM", c.date)
	}
	return line
}

func padRight(value string, width int) string {
	value = fitLine(value, width)
	if padding := width - ansi.StringWidth(value); padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}

func (m *Model) libraryLine(entry store.Series, index, width int, compact bool) string {
	cols := libraryColumns(width, compact)
	cursor := "  "
	if index == m.libCursor {
		cursor = cursorMark(true)
	}
	state := "  "
	switch {
	case !entry.Enabled:
		state = theme.Muted.Render("× ")
	case m.libSelected[entry.ID]:
		state = theme.Accent.Render("• ")
	}

	title := padRight(entryTitle(entry), cols.title)
	switch {
	case !entry.Enabled:
		title = theme.Muted.Render(title)
	case index == m.libCursor:
		title = theme.Selected.Render(title)
	}

	line := cursor + state + title
	if cols.showKind {
		line += " " + padRight(renderBadge(kindLabel(entry.Kind)), cols.kind)
	}
	if cols.showNew {
		badge := ""
		if entry.NewCount > 0 {
			badge = renderBadge(fmt.Sprintf("%d neu", entry.NewCount))
		}
		line += " " + padRight(badge, cols.newCount)
	}
	if cols.showDate {
		date := ""
		if entry.CheckedAt != nil {
			date = entry.CheckedAt.Local().Format("02.01.")
		}
		line += " " + padRight(theme.Muted.Render(date), cols.date)
	}
	if index == m.libCursor {
		return highlightRow(line, width)
	}
	return fitLine(line, width)
}

func kindLabel(kind string) string {
	switch kind {
	case store.KindSeries:
		return "Serie"
	case store.KindTatort:
		return "Tatort"
	case store.KindMovie:
		return "Film"
	}
	return "?"
}

func (m *Model) detailsView() []string {
	width := m.frameWidth()
	footer := m.detailsFooter(width)
	footer = append(m.lowerBoxes(width), footer...)
	badge := renderBadge(kindLabel(m.details.Kind))
	urlWidth := max(8, width-ansi.StringWidth(badge)-3)
	title := truncate(entryTitle(m.details), max(8, width-2))
	header := m.header(title, badge+" "+theme.Muted.Render(truncate(m.details.URL, urlWidth)))

	inner := m.cardTextWidth(width)
	open, total := 0, 0
	for _, episode := range m.episodes {
		total++
		if episode.DownloadedAt == nil {
			open++
		}
	}

	var body []string
	switch {
	case len(m.episodes) == 0 && !(m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0):
		title := "Keine Folgen gespeichert."
		hint := "Mit n in der Bibliothek prüfen."
		loading := m.detailMessage == "Folgenliste wird aktualisiert…"
		if loading {
			title = "Folgenliste wird aktualisiert."
			hint = ""
		} else if m.details.Kind == store.KindMovie {
			title = "Film - noch nicht geladen."
			hint = ""
		}
		budget := listBudget(m.height, header, footer, 0, width)
		if budget > 0 {
			if loading {
				// Placeholder rows while the list is on its way.
				body = renderCard(width, "Folgen", clampLines(renderSkeleton(inner, min(budget, 6)), budget), true, "")
				break
			}
			body = renderCard(width, "Folgen", clampLines(renderEmptyAction(inner, title, hint, "n", "in der Bibliothek prüfen"), budget), true, "")
		}
	case m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0:
		seasonOpen, seasonTotal := 0, 0
		for _, season := range m.detailSeasons {
			seasonOpen += season.open
			seasonTotal += season.total
		}
		leftWidth, rightWidth := m.splitAt(width, 0.55)
		if rightWidth > 0 {
			budget := listBudget(m.height, header, footer, 0, width)
			budget = min(budget, m.detailVisible())
			meta := fmt.Sprintf("%d Folgen · %d offen", seasonTotal, seasonOpen)
			fill := m.boxHeight(header, footer, m.detailMessageRows())
			left := renderCard(leftWidth, "Staffeln", padLines(m.seasonLines(budget, cardInner(leftWidth)), fill), true, meta)
			right := renderCard(rightWidth, "Staffel", padLines(clampLines(m.seasonDetail(cardInner(rightWidth)), budget), fill), false, "")
			body = splitColumns(left, right, leftWidth, rightWidth)
			break
		}
		showStats := m.height <= 0 || m.height >= 16
		if showStats {
			body = append(body, fitLine(strings.Join([]string{
				renderStat("Staffeln", fmt.Sprintf("%d", len(m.detailSeasons))),
				renderStat("Folgen", fmt.Sprintf("%d", seasonTotal)),
				renderStat("offen", fmt.Sprintf("%d", seasonOpen)),
			}, "  "), width))
		}
		budget := listBudget(m.height, header, footer, 3, width)
		budget = min(budget, m.detailVisible())
		rows := m.seasonLines(budget, inner)
		body = append(body, renderCard(width, "Staffeln", rows, true, "")...)
	default:
		leftWidth, rightWidth := m.splitAt(width, 0.55)
		if rightWidth > 0 {
			budget := listBudget(m.height, header, footer, 0, width)
			budget = min(budget, m.detailVisible())
			meta := fmt.Sprintf("%d %s, davon %d offen", total, episodeWord(total), open)
			fill := m.boxHeight(header, footer, m.detailMessageRows())
			left := renderCard(leftWidth, "Folgen", padLines(m.episodeLines(m.episodes, budget, cardInner(leftWidth)), fill), true, meta)
			right := renderCard(rightWidth, "Folge", padLines(clampLines(m.episodeDetail(m.episodes, cardInner(rightWidth)), budget), fill), false, "")
			body = splitColumns(left, right, leftWidth, rightWidth)
			break
		}
		if m.height <= 0 || m.height >= 16 {
			body = append(body, fitLine(fmt.Sprintf("%d %s, davon %d offen", total, episodeWord(total), open), width))
		}
		budget := listBudget(m.height, header, footer, 3, width)
		budget = min(budget, m.detailVisible())
		rows := m.episodeLines(m.episodes, budget, inner)
		body = append(body, renderCard(width, "Folgen", rows, true, "")...)
	}
	body = m.appendDetailMessage(body, len(header), len(footer), width)
	return m.frameRows(header, body, footer)
}

func (m *Model) seasonLines(budget, width int) []string {
	if budget <= 0 {
		return nil
	}
	var lines []string
	start, end, top, bottom := windowRange(len(m.detailSeasons), m.detailSeasonCursor, m.detailOffset, budget)
	if top {
		lines = append(lines, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
	}
	for index := start; index < end; index++ {
		season := m.detailSeasons[index]
		cursor := "  "
		if index == m.detailSeasonCursor {
			cursor = cursorMark(true)
		}
		line := fmt.Sprintf("Staffel %d - %d %s, %d offen",
			season.number, season.total, episodeWord(season.total), season.open)
		if index == m.detailSeasonCursor {
			line = theme.Selected.Render(line)
		}
		row := fitLine(cursor+line, width)
		if index == m.detailSeasonCursor {
			row = highlightRow(row, width)
		}
		lines = append(lines, row)
	}
	if bottom {
		lines = append(lines, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(m.detailSeasons)-end)))
	}
	return lines
}

func (m *Model) detailsFooter(width int) []string {
	return m.screenFooter(width)
}

func (m *Model) episodeFooter(width int, inSeason bool) []string {
	return m.screenFooter(width)
}

func (m *Model) seasonEpisodesView() []string {
	width := m.frameWidth()
	footer := m.episodeFooter(width, true)
	footer = append(m.lowerBoxes(width), footer...)
	badge := renderBadge(kindLabel(m.details.Kind))
	seasonLabel := fmt.Sprintf("Staffel %d  ", m.activeDetailSeason)
	urlWidth := max(8, width-ansi.StringWidth(badge)-ansi.StringWidth(seasonLabel)-3)
	title := truncate(entryTitle(m.details), max(8, width-2))
	header := m.header(title, seasonLabel+badge+" "+theme.Muted.Render(truncate(m.details.URL, urlWidth)))

	open := 0
	for _, episode := range m.detailSeasonEpisodes {
		if episode.DownloadedAt == nil {
			open++
		}
	}
	total := len(m.detailSeasonEpisodes)
	var body []string
	if m.height <= 0 || m.height >= 16 {
		body = append(body, fitLine(fmt.Sprintf("%d %s, davon %d offen", total, episodeWord(total), open), width))
	}

	leftWidth, rightWidth := m.splitAt(width, 0.55)
	// The list is measured for the box it stands in.
	inner := m.cardTextWidth(width)
	if rightWidth > 0 {
		inner = cardInner(leftWidth)
	}
	budget := listBudget(m.height, header, footer, 2, width)
	budget = min(budget, m.detailVisible())
	var rows []string
	if total == 0 {
		rows = clampLines(renderEmptyAction(inner, "Keine Folgen in dieser Staffel gespeichert.", "", "esc", "zurück"), budget)
	} else {
		rows = m.episodeLines(m.detailSeasonEpisodes, budget, inner)
	}
	if rightWidth > 0 {
		// The episode under the cursor gets its own column.
		meta := fmt.Sprintf("%d %s, davon %d offen", total, episodeWord(total), open)
		fill := m.boxHeight(header, footer, m.detailMessageRows())
		left := renderCard(leftWidth, "Folgen", padLines(rows, fill), true, meta)
		right := renderCard(rightWidth, "Folge", padLines(clampLines(m.episodeDetail(m.detailSeasonEpisodes, cardInner(rightWidth)), budget), fill), false, "")
		body = splitColumns(left, right, leftWidth, rightWidth)
	} else {
		body = append(body, renderCard(width, "Folgen", rows, true, "")...)
	}
	body = m.appendDetailMessage(body, len(header), len(footer), width)
	return m.frameRows(header, body, footer)
}

// seasonDetail describes the season under the cursor.
func (m *Model) seasonDetail(width int) []string {
	if len(m.detailSeasons) == 0 {
		return renderEmptyAction(width, "Keine Staffel.", "", "n", "in der Bibliothek prüfen")
	}
	season := m.detailSeasons[min(m.detailSeasonCursor, len(m.detailSeasons)-1)]
	loaded := season.total - season.open
	lines := []string{theme.Title.Render(fmt.Sprintf("Staffel %d", season.number))}
	lines = append(lines, "")
	lines = append(lines, detailRows("Folgen", fmt.Sprintf("%d", season.total), width)...)
	lines = append(lines, detailRows("Geladen", fmt.Sprintf("%d", loaded), width)...)
	lines = append(lines, detailRows("Offen", fmt.Sprintf("%d", season.open), width)...)
	if season.total > 0 {
		lines = append(lines, "")
		barWidth := min(max(width-4, 8), 40)
		lines = append(lines, fitLine(progressBar(loaded, season.total, barWidth), width))
	}
	return lines
}

// episodeDetail describes the episode under the cursor.
func (m *Model) episodeDetail(episodes []store.KnownEpisode, width int) []string {
	if len(episodes) == 0 {
		return renderEmptyAction(width, "Keine Folge.", "", "esc", "zurück")
	}
	episode := episodes[min(m.detailCursor, len(episodes)-1)]
	lines := []string{}
	for _, line := range wrapText(episode.Title, m.flowWidth(width)) {
		lines = append(lines, theme.Title.Render(line))
	}
	status := renderPill("offen", pillOpen)
	if episode.DownloadedAt != nil {
		status = renderPill("geladen", pillDone)
	}
	label := episodeLabel(episode)
	if label == "-" {
		lines = append(lines, status)
	} else {
		lines = append(lines, label+"  "+status)
	}
	lines = append(lines, "")
	if !episode.FirstSeen.IsZero() {
		lines = append(lines, detailRows("Erstmals", episode.FirstSeen.Local().Format("02.01.2006 15:04"), width)...)
	}
	if episode.DownloadedAt != nil {
		lines = append(lines, detailRows("Geladen", episode.DownloadedAt.Local().Format("02.01.2006 15:04"), width)...)
	}
	return lines
}

func (m *Model) episodeLines(episodes []store.KnownEpisode, budget, width int) []string {
	if budget <= 0 {
		return nil
	}
	numbered := false
	for _, episode := range episodes {
		if episode.Season > 0 || episode.Number > 0 {
			numbered = true
			break
		}
	}
	var lines []string
	start, end, top, bottom := windowRange(len(episodes), m.detailCursor, m.detailOffset, budget)
	if top {
		lines = append(lines, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
	}
	for index := start; index < end; index++ {
		episode := episodes[index]
		cursor := "  "
		if index == m.detailCursor {
			cursor = cursorMark(true)
		}
		box := theme.Subtle.Render("[ ]")
		if m.detailChoice[index] {
			box = theme.Accent.Render("[x]")
		}
		status := renderPill("geladen", pillDone)
		if episode.DownloadedAt == nil {
			status = renderPill("offen", pillOpen)
		}
		label := ""
		if numbered {
			label = theme.Muted.Render(fmt.Sprintf("%-8s", episodeLabel(episode)))
		}
		fixed := ansi.StringWidth(cursor) + 3 + 1 + ansi.StringWidth(label) + 1 + ansi.StringWidth(status)
		titleWidth := max(4, width-fixed)
		episodeTitle := fitLine(episode.Title, titleWidth)
		if index == m.detailCursor {
			episodeTitle = theme.Selected.Render(episodeTitle)
		}
		line := fitLine(fmt.Sprintf("%s%s %s%s %s", cursor, box, label, episodeTitle, status), width)
		if index == m.detailCursor {
			line = highlightRow(line, width)
		}
		lines = append(lines, line)
	}
	if bottom {
		lines = append(lines, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(episodes)-end)))
	}
	return lines
}

func episodeLabel(episode store.KnownEpisode) string {
	if episode.Season > 0 && episode.Number > 0 {
		return fmt.Sprintf("S%02dE%02d", episode.Season, episode.Number)
	}
	if episode.Number > 0 {
		return fmt.Sprintf("Folge %d", episode.Number)
	}
	return "-"
}
