package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) queueView() string {
	width := m.contentWidth()
	jobIndex := m.currentJobIndex()
	position := 0
	if jobIndex >= 0 {
		position = jobIndex + 1
	}
	header := renderHeader(width, "Downloads", fmt.Sprintf("Datei %d von %d", position, len(m.jobs)))
	footer := renderFooter(width, []keyHint{
		{Key: "q", Label: "abbrechen"},
		{Key: "ctrl+c", Label: "beenden"},
	})

	maxContent := 1 << 30
	if m.height > 0 {
		reserved := len(header) + len(footer)
		if m.height >= 18 {
			// Blank lines above and between the panels.
			reserved += 2
		}
		if m.cancelled {
			reserved += 2
		}
		maxContent = max(0, m.height-reserved)
	}

	spacious := m.height <= 0 || m.height >= 18
	var contentLines []string
	if spacious {
		contentLines = append(contentLines, "")
	}
	download := m.downloadPanel(width, maxContent)
	contentLines = append(contentLines, download...)
	remaining := maxContent - len(download)
	if remaining > 0 {
		if spacious && remaining > 1 {
			contentLines = append(contentLines, "")
			remaining--
		}
		contentLines = append(contentLines, m.activityPanel(width, remaining)...)
	}

	if m.cancelled {
		contentLines = append(contentLines, "", fitLine(theme.Danger.Render("Abbruch angefordert…"), width))
	}
	return m.renderFrame(header, contentLines, footer)
}

func (m *Model) downloadPanel(width, maxLines int) []string {
	inner := panelInner(width)
	content := []string{}
	jobIndex := m.currentJobIndex()
	if jobIndex < 0 {
		content = append(content, fitLine(theme.Muted.Render("Download wird vorbereitet…"), inner))
	} else {
		item := m.jobs[jobIndex]
		finished, failed := 0, 0
		for _, current := range m.jobs {
			switch current.status {
			case "fertig":
				finished++
			case "fehlgeschlagen":
				failed++
			}
		}
		kind := pillNeutral
		switch item.status {
		case "lädt":
			kind = pillOpen
		case "fertig":
			kind = pillDone
		case "fehlgeschlagen":
			kind = pillError
		}

		pill := renderPill(item.status, kind)
		label := progressLabel(item.done, item.total)
		pillWidth := ansi.StringWidth(pill)
		labelWidth := ansi.StringWidth(label)
		if pillWidth+labelWidth+2 <= inner {
			content = append(content, fitLine(pill+strings.Repeat(" ", inner-pillWidth-labelWidth)+label, inner))
		} else {
			content = append(content, fitLine(pill, inner))
			content = append(content, fitLine(label, inner))
		}

		content = append(content, fitLine(theme.Muted.Render("Datei")+"  "+item.label, inner))
		if item.total > 0 {
			barWidth := min(max(inner-4, 8), 60)
			content = append(content, fitLine(progressBar(item.done, item.total, barWidth), inner))
		}

		showDetails := m.height <= 0 || m.height >= 18
		if showDetails && item.path != "" {
			content = append(content, fitLine(theme.Muted.Render("Ziel")+"  "+item.path, inner))
		}
		if showDetails && (finished > 0 || failed > 0) {
			counter := fmt.Sprintf("%d fertig", finished)
			if failed > 0 {
				counter += fmt.Sprintf(" · %d fehlgeschlagen", failed)
			}
			content = append(content, fitLine(counter, inner))
		}
	}
	fitted := m.renderPanelBudget(width, "Aktueller Download", content, maxLines)
	return fitted
}

// renderPanelBudget shrinks the content until the bordered panel fits the
// available height.
func (m *Model) renderPanelBudget(width int, title string, content []string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 1 << 30
	}
	if maxLines < panelOverhead(width, title) {
		return nil
	}
	for len(content) > 0 {
		panel := renderPanel(width, title, content, true)
		if len(panel) <= maxLines {
			return panel
		}
		content = content[:len(content)-1]
	}
	return renderPanel(width, title, content, true)
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
	inner := panelInner(width)
	overhead := panelOverhead(width, "Aktivität")
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
	return m.renderPanelBudget(width, "Aktivität", content, maxLines)
}

func (m *Model) hasCompletedJob() bool {
	for _, item := range m.jobs {
		if item.status == "fertig" || item.status == "fehlgeschlagen" {
			return true
		}
	}
	return false
}

func (m *Model) libraryView() string {
	width := m.contentWidth()
	subtitle := librarySubtitle(m.library)
	header := renderHeader(width, "Bibliothek", subtitle)
	footer := renderFooter(width, []keyHint{
		{Key: "enter", Label: "öffnen"},
		{Key: "d", Label: "laden"},
		{Key: "n", Label: "prüfen"},
		{Key: "space", Label: "aktiv"},
		{Key: "a", Label: "URL"},
		{Key: "s", Label: "Einstellungen"},
		{Key: "x", Label: "entfernen"},
		{Key: "q", Label: "beenden"},
	})

	inner := panelInner(width)
	extra := 1 // column heading inside the panel
	if m.libMessage != "" {
		extra += 2
	}
	budget := listBudget(m.height, header, footer, extra, width, "Serien")
	if m.height <= 0 {
		budget = len(m.library)
	}
	budget = min(budget, m.libVisible())

	var rows []string
	if len(m.library) == 0 {
		rows = clampLines(renderEmpty(inner, "Noch keine Serien gespeichert.", "Mit a eine URL hinzufügen."), budget)
	} else {
		rows = append(rows, theme.Muted.Render(fitLine(libraryColumns(inner).header(), inner)))
	}
	if len(m.library) > 0 && budget > 0 {
		start, end, top, bottom := windowRange(len(m.library), m.libCursor, m.libOffset, budget)
		if top {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
		}
		for index := start; index < end; index++ {
			rows = append(rows, m.libraryLine(m.library[index], index, inner))
		}
		if bottom {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(m.library)-end)))
		}
	}
	panel := renderPanel(width, "Serien", rows, true)

	body := panel
	if m.libMessage != "" {
		body = append(body, "", fitLine(theme.Muted.Render(m.libMessage), width))
	}
	return m.renderFrame(header, body, footer)
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
	showNew  bool
	showDate bool
}

func libraryColumns(width int) libraryCols {
	cols := libraryCols{kind: 8}
	cols.showNew = width >= 56
	cols.showDate = width >= 84
	if cols.showNew {
		cols.newCount = 8
	}
	if cols.showDate {
		cols.date = 6
	}
	fixed := 4 + 1 + cols.kind
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
	line := "    " + padRight("TITEL", c.title) + " " + padRight("TYP", c.kind)
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

func (m *Model) libraryLine(entry store.Series, index, width int) string {
	cols := libraryColumns(width)
	cursor := "  "
	if index == m.libCursor {
		cursor = theme.Accent.Render("▸ ")
	}
	state := "  "
	if !entry.Enabled {
		state = theme.Muted.Render("× ")
	}

	title := padRight(entryTitle(entry), cols.title)
	switch {
	case !entry.Enabled:
		title = theme.Muted.Render(title)
	case index == m.libCursor:
		title = theme.Selected.Render(title)
	}

	line := cursor + state + title + " " + padRight(renderBadge(kindLabel(entry.Kind)), cols.kind)
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

func (m *Model) detailsView() string {
	width := m.contentWidth()
	footer := m.detailsFooter(width)
	badge := renderBadge(kindLabel(m.details.Kind))
	urlWidth := max(8, width-ansi.StringWidth(badge)-3)
	title := truncate(entryTitle(m.details), max(8, width-2))
	header := renderHeader(width, title, badge+" "+theme.Muted.Render(truncate(m.details.URL, urlWidth)))

	inner := panelInner(width)
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
		if m.detailMessage == "Folgenliste wird aktualisiert…" {
			title = "Folgenliste wird aktualisiert."
			hint = ""
		} else if m.details.Kind == store.KindMovie {
			title = "Film - noch nicht geladen."
			hint = ""
		}
		budget := listBudget(m.height, header, footer, 0, width, "Folgen")
		if budget > 0 {
			body = renderPanel(width, "Folgen", clampLines(renderEmpty(inner, title, hint), budget), true)
		}
	case m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0:
		seasonOpen, seasonTotal := 0, 0
		for _, season := range m.detailSeasons {
			seasonOpen += season.open
			seasonTotal += season.total
		}
		showStats := m.height <= 0 || m.height >= 16
		if showStats {
			body = append(body, fitLine(strings.Join([]string{
				renderStat("Staffeln", fmt.Sprintf("%d", len(m.detailSeasons))),
				renderStat("Folgen", fmt.Sprintf("%d", seasonTotal)),
				renderStat("offen", fmt.Sprintf("%d", seasonOpen)),
			}, "  "), width))
		}
		budget := listBudget(m.height, header, footer, 3, width, "Staffeln")
		budget = min(budget, m.detailVisible())
		rows := m.seasonLines(budget, inner)
		body = append(body, renderPanel(width, "Staffeln", rows, true)...)
	default:
		if m.height <= 0 || m.height >= 16 {
			body = append(body, fitLine(fmt.Sprintf("%d %s, davon %d offen", total, episodeWord(total), open), width))
		}
		budget := listBudget(m.height, header, footer, 3, width, "Folgen")
		budget = min(budget, m.detailVisible())
		rows := m.episodeLines(m.episodes, budget, inner)
		body = append(body, renderPanel(width, "Folgen", rows, true)...)
	}
	body = m.appendDetailMessage(body, len(header), len(footer), width)
	return m.renderFrame(header, body, footer)
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
			cursor = theme.Accent.Render("▸ ")
		}
		line := fmt.Sprintf("Staffel %d - %d %s, %d offen",
			season.number, season.total, episodeWord(season.total), season.open)
		if index == m.detailSeasonCursor {
			line = theme.Selected.Render(line)
		}
		lines = append(lines, fitLine(cursor+line, width))
	}
	if bottom {
		lines = append(lines, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(m.detailSeasons)-end)))
	}
	return lines
}

func (m *Model) detailsFooter(width int) []string {
	series := m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0
	var hints []keyHint
	if series {
		hints = []keyHint{
			{Key: "up/down", Label: "Staffel"},
			{Key: "enter", Label: "Folgen"},
			{Key: "d", Label: "Staffel laden"},
			{Key: "esc", Label: "Bibliothek"},
			{Key: "?", Label: "Hilfe"},
		}
	} else {
		hints = []keyHint{
			{Key: "space", Label: "wählen"},
			{Key: "a", Label: "alle/keine"},
			{Key: "d", Label: "laden"},
			{Key: "esc", Label: "Bibliothek"},
			{Key: "?", Label: "Hilfe"},
		}
	}
	return renderFooter(width, hints)
}

func (m *Model) episodeFooter(width int, inSeason bool) []string {
	back := "Bibliothek"
	if inSeason {
		back = "Staffeln"
	}
	return renderFooter(width, []keyHint{
		{Key: "space", Label: "wählen"},
		{Key: "a", Label: "alle/keine"},
		{Key: "d", Label: "laden"},
		{Key: "esc", Label: back},
		{Key: "?", Label: "Hilfe"},
	})
}

func (m *Model) seasonEpisodesView() string {
	width := m.contentWidth()
	footer := m.episodeFooter(width, true)
	badge := renderBadge(kindLabel(m.details.Kind))
	seasonLabel := fmt.Sprintf("Staffel %d  ", m.activeDetailSeason)
	urlWidth := max(8, width-ansi.StringWidth(badge)-ansi.StringWidth(seasonLabel)-3)
	title := truncate(entryTitle(m.details), max(8, width-2))
	header := renderHeader(width, title, seasonLabel+badge+" "+theme.Muted.Render(truncate(m.details.URL, urlWidth)))

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

	inner := panelInner(width)
	budget := listBudget(m.height, header, footer, 2, width, "Folgen")
	budget = min(budget, m.detailVisible())
	var rows []string
	if total == 0 {
		rows = clampLines(renderEmpty(inner, "Keine Folgen in dieser Staffel gespeichert.", ""), budget)
	} else {
		rows = m.episodeLines(m.detailSeasonEpisodes, budget, inner)
	}
	body = append(body, renderPanel(width, "Folgen", rows, true)...)
	body = m.appendDetailMessage(body, len(header), len(footer), width)
	return m.renderFrame(header, body, footer)
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
			cursor = theme.Accent.Render("▸ ")
		}
		box := "[ ]"
		if m.detailChoice[index] {
			box = selectedStyle.Render("[x]")
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
		line := fmt.Sprintf("%s%s %s%s %s", cursor, box, label, episodeTitle, status)
		lines = append(lines, fitLine(line, width))
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
