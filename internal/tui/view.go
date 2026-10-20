package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/revunix/ard-plus-dl/internal/store"
)

const maxContentWidth = 120

// View renders the current screen.
func (m *Model) View() string {
	if m.helpVisible {
		return m.helpView()
	}
	if m.width > 0 && (m.width < 40 || (m.height > 0 && m.height < 12)) {
		return fitLine(centerLine("Terminal zu klein (mindestens 40x12).", m.width), m.width)
	}
	switch m.screen {
	case ScreenStart:
		return m.startView()
	case ScreenSeries:
		return m.seriesView()
	case ScreenTatort:
		return m.tatortView()
	case ScreenQueue, ScreenLoading:
		return m.queueView()
	case ScreenSummary:
		return m.summaryView()
	case ScreenLibrary:
		return m.libraryView()
	case ScreenDetails:
		return m.detailsView()
	case ScreenSeasonEpisodes:
		return m.seasonEpisodesView()
	}
	return ""
}

// contentWidth keeps the layout readable on very wide terminals.
func (m *Model) contentWidth() int {
	if m.width > 0 && m.width < maxContentWidth {
		return m.width
	}
	return maxContentWidth
}

// frameLines fits every line to the content width and clips the height.
func (m *Model) frameLines(lines []string) string {
	width := m.contentWidth()
	fitted := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted = append(fitted, fitLine(line, width))
	}
	if m.height > 0 && len(fitted) > m.height {
		fitted = fitted[:m.height]
	}
	return strings.Join(fitted, "\n")
}

// renderFrame joins header, body and footer and keeps the footer visible when
// the terminal is too short for everything.
func (m *Model) renderFrame(header, body, footer []string) string {
	if m.height > 0 {
		overflow := len(header) + len(body) + len(footer) - m.height
		if overflow > 0 {
			if overflow >= len(body) {
				body = nil
			} else {
				body = body[:len(body)-overflow]
			}
		}
	}
	lines := make([]string, 0, len(header)+len(body)+len(footer))
	lines = append(lines, header...)
	lines = append(lines, body...)
	lines = append(lines, footer...)
	return m.frameLines(lines)
}

// panelInner is the content width inside a bordered panel. The two cells of
// slack cover the optional panel padding.
func panelInner(width int) int {
	return max(1, width-4)
}

func panelOverhead(width int, title string) int {
	return len(renderPanel(width, title, nil, false))
}

// listBudget returns the number of list rows that fit next to the fixed chrome.
func listBudget(height int, header, footer []string, extra int, width int, panelTitle string) int {
	if height <= 0 {
		return 1 << 30
	}
	available := height - len(header) - len(footer) - extra - panelOverhead(width, panelTitle)
	return max(0, available)
}

// clampLines keeps at most budget rows, so a panel never grows past its slot.
func clampLines(lines []string, budget int) []string {
	if budget < 0 {
		budget = 0
	}
	if len(lines) > budget {
		return lines[:budget]
	}
	return lines
}

func fitInner(lines []string, width int) []string {
	for index, line := range lines {
		lines[index] = fitLine(line, width)
	}
	return lines
}

func dangerText(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "fehler") || strings.Contains(lower, "error") ||
		strings.Contains(lower, "konnte nicht") || strings.Contains(lower, "fehlgeschlagen")
}

func tailWithEllipsis(lines []string, budget int) []string {
	if budget >= len(lines) {
		return lines
	}
	if budget <= 0 {
		return nil
	}
	tail := append([]string(nil), lines[len(lines)-budget:]...)
	tail[0] = "… " + tail[0]
	return tail
}

func (m *Model) helpView() string {
	width := m.contentWidth()
	header := renderHeader(width, "Hilfe", "Tasten und Befehle")
	hintLine := func(innerWidth int, pairs ...string) string {
		parts := make([]string, 0, len(pairs)/2)
		for index := 0; index+1 < len(pairs); index += 2 {
			parts = append(parts, theme.Key.Render(pairs[index])+" "+theme.Muted.Render(pairs[index+1]))
		}
		return fitLine("  "+strings.Join(parts, " · "), innerWidth)
	}
	groups := func(innerWidth int) [][]string {
		return [][]string{
			{
				renderSection("Bibliothek"),
				hintLine(innerWidth, "enter", "öffnen", "d", "laden", "n", "prüfen"),
				hintLine(innerWidth, "space", "aktiv", "a", "URL", "s", "Einstellungen", "x", "entfernen"),
				hintLine(innerWidth, "q", "beenden"),
			},
			{
				renderSection("Staffeln/Folgen"),
				hintLine(innerWidth, "up/down", "wählen", "space", "markieren", "a", "alle/keine"),
				hintLine(innerWidth, "enter/d", "laden", "esc", "zurück"),
			},
			{
				renderSection("Download"),
				hintLine(innerWidth, "q/ctrl+c", "abbrechen, erneut beenden"),
			},
			{
				renderSection("Start"),
				hintLine(innerWidth, "tab", "Feld", "enter", "starten/speichern"),
				hintLine(innerWidth, "esc", "zurück", "ctrl+c", "beenden"),
			},
			{
				renderSection("Fenster"),
				hintLine(innerWidth, "?", "Hilfe schließen"),
			},
		}
	}

	inner := panelInner(width)
	var content []string
	if width >= 120 {
		// Two columns keep the help compact on wide terminals.
		leftWidth := (inner - 2) / 2
		rightWidth := inner - leftWidth - 2
		leftGroups := groups(leftWidth)
		rightGroups := groups(rightWidth)
		var left, right []string
		for _, group := range leftGroups[:2] {
			left = append(left, group...)
		}
		for _, group := range rightGroups[2:] {
			right = append(right, group...)
		}
		content = strings.Split(joinColumns(left, right, leftWidth, rightWidth), "\n")
	} else {
		for _, group := range groups(inner) {
			content = append(content, group...)
		}
	}
	footer := renderFooter(width, []keyHint{{Key: "esc", Label: "Hilfe schließen"}})
	budget := listBudget(m.height, header, footer, 0, width, "Übersicht")
	if budget > 0 && len(content) > budget {
		content = content[:budget]
	}
	panel := renderPanel(width, "Übersicht", fitInner(content, inner), true)
	return m.renderFrame(header, panel, footer)
}

func (m *Model) startView() string {
	width := m.contentWidth()
	title := "Start"
	subtitle := "Zugang und Download-Ziel"
	switch m.formPurpose {
	case formSettings:
		subtitle = "Einstellungen"
	case formAddURL:
		subtitle = "Neue URL hinzufügen"
	}
	header := renderHeader(width, title, subtitle)

	labels := []string{"URL", "Benutzer", "Passwort", "Datei-Tag"}
	rows := make([]string, 0, len(m.fields))
	for index, field := range m.fields {
		marker := "  "
		labelStyle := theme.Muted
		if index == m.focus {
			marker = theme.Accent.Render("▸ ")
			labelStyle = theme.Label
		}
		row := marker + labelStyle.Render(fmt.Sprintf("%-10s", labels[index])) + " " + field.View()
		rows = append(rows, row)
	}

	action := "starten"
	if m.cfg.URL == "" && len(m.library) > 0 {
		action = "speichern"
	}
	footer := renderFooter(width, []keyHint{
		{Key: "tab", Label: "Feld"},
		{Key: "enter", Label: action},
		{Key: "esc", Label: "zurück"},
		{Key: "ctrl+c", Label: "beenden"},
	})

	var errorLines []string
	if m.startErr != nil {
		errorLines = wrapText(m.startErr.Error(), max(1, width-2))
	}

	panel := renderPanel(width, "Zugang", fitInner(rows, panelInner(width)), true)
	body := panel
	if m.height > 0 && len(header)+len(panel)+len(footer) > m.height {
		// Very short terminals: drop the box so all four fields stay visible.
		plain := make([]string, 0, len(rows))
		for _, row := range rows {
			plain = append(plain, fitLine(row, width))
		}
		body = plain
	}
	if len(errorLines) > 0 {
		// Reserve the blank separator and at least one line for the cause.
		available := 1 << 30
		if m.height > 0 {
			available = m.height - len(header) - len(footer) - len(body) - 1
		}
		if available < 1 {
			// The panel no longer fits beside the error: keep the four fields
			// and the tail of the error instead of losing the cause.
			plain := make([]string, 0, len(rows))
			for _, row := range rows {
				plain = append(plain, fitLine(row, width))
			}
			body = plain
			if m.height > 0 {
				available = m.height - len(header) - len(footer) - len(plain) - 1
			}
		}
		errorLines = tailWithEllipsis(errorLines, max(1, available))
		if m.height <= 0 || m.height >= 18 {
			body = append(body, "")
		}
		for _, line := range errorLines {
			body = append(body, fitLine(theme.Danger.Render(line), width))
		}
	}
	return m.renderFrame(header, body, footer)
}

func (m *Model) seriesView() string {
	width := m.contentWidth()
	heading := m.showName
	if heading == "" {
		heading = "Staffeln"
	}
	heading = truncate(heading, max(8, width-2))
	selected := 0
	for _, season := range m.seasons {
		if m.selection[season.Number] {
			selected++
		}
	}
	header := renderHeader(width, heading, fmt.Sprintf("%d ausgewählt", selected))

	panelTitle := "Staffeln"
	if m.categoryMode {
		panelTitle = "Kategorien"
	}
	footerHints := []keyHint{
		{Key: "space", Label: "wählen"},
		{Key: "a", Label: "alle/keine"},
		{Key: "enter", Label: "starten"},
		{Key: "?", Label: "Hilfe"},
		{Key: "ctrl+c", Label: "beenden"},
	}
	if m.categoryMode {
		footerHints[2] = keyHint{Key: "enter", Label: "übernehmen"}
	}
	footer := renderFooter(width, footerHints)

	inner := panelInner(width)
	budget := listBudget(m.height, header, footer, 0, width, panelTitle)
	budget = min(budget, m.seriesVisible())
	// The hint about an empty selection needs a line of its own.
	hintLines := 0
	if m.selectionErr != "" {
		hintLines = len(wrapText(m.selectionErr, inner))
		budget = max(1, budget-hintLines)
	}
	var rows []string
	if len(m.seasons) == 0 {
		rows = clampLines(renderEmpty(inner, "Keine Staffeln verfügbar.", "Bitte später erneut prüfen."), budget)
	} else {
		start, end, top, bottom := windowRange(len(m.seasons), m.cursor, m.seriesOffset, budget)
		if top {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
		}
		for index := start; index < end; index++ {
			season := m.seasons[index]
			cursor := "  "
			if index == m.cursor {
				cursor = theme.Accent.Render("▸ ")
			}
			box := "[ ]"
			if m.selection[season.Number] {
				box = theme.Selected.Render("[x]")
			}
			label := fmt.Sprintf("Staffel %d - %s", season.Number, season.Title)
			if m.categoryMode {
				label = season.Title
			}
			if index == m.cursor {
				label = theme.Selected.Render(label)
			}
			rows = append(rows, fitLine(cursor+box+" "+label, inner))
		}
		if bottom {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", len(m.seasons)-end)))
		}
	}
	if m.selectionErr != "" {
		rows = append(rows, "")
		for _, line := range wrapText(m.selectionErr, inner) {
			rows = append(rows, fitLine(theme.Danger.Render(line), inner))
		}
	}
	panel := renderPanel(width, panelTitle, rows, true)
	return m.renderFrame(header, panel, footer)
}

func (m *Model) tatortView() string {
	width := m.contentWidth()
	title := truncate("Tatort "+m.tatortCity, max(8, width-2))
	header := renderHeader(width, title, "Episoden überspringen")
	footer := renderFooter(width, []keyHint{
		{Key: "enter", Label: "starten"},
		{Key: "ctrl+c", Label: "beenden"},
	})

	inner := panelInner(width)
	question := fmt.Sprintf("Wie viele Episoden überspringen? (0 bis %d, 0 = alle laden)", m.tatortCount)
	questionLines := wrapText(question, inner)
	fieldLine := fitLine(m.skipField.View(), inner)

	if m.skipErr == "" {
		content := append(fitInner(questionLines, inner), "")
		content = append(content, fieldLine)
		return m.renderFrame(header, renderPanel(width, "Überspringen", content, true), footer)
	}

	available := listBudget(m.height, header, footer, 0, width, "Überspringen")
	// Trim the question until the field and at least one error line fit.
	for len(questionLines) > 0 && len(questionLines)+2 > available {
		questionLines = questionLines[:len(questionLines)-1]
	}
	content := fitInner(questionLines, inner)
	content = append(content, fieldLine)
	errorBudget := available - len(content) - 1
	blank := true
	if errorBudget < 1 {
		blank = false
		errorBudget = available - len(content)
	}
	if errorBudget < 1 {
		errorBudget = 1
	}
	if blank {
		content = append(content, "")
	}
	for _, line := range tailWithEllipsis(wrapText(m.skipErr, inner), errorBudget) {
		content = append(content, fitLine(theme.Danger.Render(line), inner))
	}
	return m.renderFrame(header, renderPanel(width, "Überspringen", content, true), footer)
}

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

func joinColumns(left, right []string, leftWidth, rightWidth int) string {
	rows := max(len(left), len(right))
	var b strings.Builder
	for index := range rows {
		leftLine, rightLine := "", ""
		if index < len(left) {
			leftLine = fitLine(left[index], leftWidth)
		}
		if index < len(right) {
			rightLine = fitLine(right[index], rightWidth)
		}
		padding := leftWidth - ansi.StringWidth(leftLine)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(leftLine + strings.Repeat(" ", padding) + "  " + rightLine)
		if index+1 < rows {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

// wrapText wraps value to width, preserving ANSI sequences and wide runes.
func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	return strings.Split(ansi.Wrap(value, width, " /-"), "\n")
}

// fitLogLine keeps the end of a log line so the actual error cause stays visible.
func fitLogLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	valueWidth := ansi.StringWidth(value)
	if valueWidth <= width {
		return value
	}
	trim := valueWidth - width + ansi.StringWidth("…")
	return ansi.TruncateLeft(value, trim, "…")
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

func (m *Model) summaryView() string {
	width := m.contentWidth()
	title := "Fertig"
	if m.doneErr != nil || m.cancelled {
		title = "Abgebrochen"
	}
	subtitle := "Zusammenfassung"
	header := renderHeader(width, title, subtitle)
	footer := renderFooter(width, []keyHint{
		{Key: "l", Label: "Bibliothek"},
		{Key: "q", Label: "beenden"},
		{Key: "enter", Label: "beenden"},
	})

	inner := panelInner(width)
	symbol := theme.Success.Render("✓")
	message := []string{"Alle Downloads abgeschlossen."}
	messageStyle := theme.Success
	if m.doneErr != nil {
		symbol = theme.Danger.Render("!")
		message = wrapText(m.doneErr.Error(), inner)
		messageStyle = theme.Danger
	}

	finished, failed := 0, 0
	for _, item := range m.jobs {
		switch item.status {
		case "fertig":
			finished++
		case "fehlgeschlagen":
			failed++
		}
	}
	counters := []string{fmt.Sprintf("%d von %d Downloads abgeschlossen.", finished, len(m.jobs))}
	if failed > 0 {
		counters = append(counters, theme.Danger.Render(fmt.Sprintf("%d %s fehlgeschlagen", failed, fileWord(failed))))
	}

	available := 1 << 30
	if m.height > 0 {
		available = m.height - len(header) - len(footer) - panelOverhead(width, "Ergebnis")
	}
	if available < 1 {
		available = 1
	}
	remaining := available - 1 // the symbol
	msgBudget := remaining
	if remaining >= 2 {
		msgBudget = remaining - len(counters)
	}
	if msgBudget < 1 {
		msgBudget = remaining
		counters = nil
	}

	rows := []string{symbol}
	for _, line := range tailWithEllipsis(styleLines(message, messageStyle), msgBudget) {
		rows = append(rows, fitLine(line, inner))
	}
	for _, line := range counters {
		rows = append(rows, fitLine(line, inner))
	}
	panel := renderPanel(width, "Ergebnis", rows, true)
	return m.renderFrame(header, panel, footer)
}

func styleLines(lines []string, style interface{ Render(...string) string }) []string {
	rendered := make([]string, len(lines))
	for index, line := range lines {
		rendered[index] = style.Render(line)
	}
	return rendered
}

func (m *Model) appendDetailMessage(lines []string, headerLen, footerLen, width int) []string {
	if m.detailMessage == "" {
		return lines
	}
	message := styleLines(wrapText(m.detailMessage, max(1, panelInner(width))), theme.Muted)
	if dangerText(m.detailMessage) {
		message = styleLines(wrapText(m.detailMessage, max(1, panelInner(width))), theme.Danger)
	}
	if m.height > 0 {
		budget := m.height - headerLen - footerLen - len(lines) - 1
		if budget < 1 {
			budget = m.height - headerLen - footerLen - len(lines)
		}
		message = tailWithEllipsis(message, budget)
		if len(message) == 0 {
			return lines
		}
	}
	lines = append(lines, "")
	for _, line := range message {
		lines = append(lines, fitLine(line, width))
	}
	return lines
}

func (m *Model) tailLog(count int) []string {
	if len(m.log) <= count {
		return m.log
	}
	return m.log[len(m.log)-count:]
}

// windowRange returns the visible slice and whether list markers are needed.
func windowRange(total, cursor, offset, budget int) (start, end int, top, bottom bool) {
	if total <= 0 || budget <= 0 {
		return 0, 0, false, false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		offset = total - 1
	}
	start = offset
	if cursor < start {
		start = cursor
	}
	end = start + budget
	if end > total {
		end = total
	}
	if cursor >= end {
		end = cursor + 1
		if end > total {
			end = total
		}
		start = end - budget
		if start < 0 {
			start = 0
		}
	}
	top = start > 0
	bottom = end < total
	if budget < 3 {
		// With one or two rows, the cursor and the rows matter more than the
		// "more" markers.
		top = false
		bottom = false
	}
	for end-start+markerCount(top, bottom) > budget {
		if end > cursor+1 {
			end--
			bottom = end < total
			continue
		}
		if bottom {
			bottom = false
			continue
		}
		if top {
			top = false
			continue
		}
		break
	}
	return start, end, top, bottom
}

func markerCount(top, bottom bool) int {
	count := 0
	if top {
		count++
	}
	if bottom {
		count++
	}
	return count
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	return fitLine(value, max)
}

func fileWord(count int) string {
	if count == 1 {
		return "Datei"
	}
	return "Dateien"
}

func episodeWord(count int) string {
	if count == 1 {
		return "Folge"
	}
	return "Folgen"
}

func seasonWord(count int) string {
	if count == 1 {
		return "Staffel"
	}
	return "Staffeln"
}
