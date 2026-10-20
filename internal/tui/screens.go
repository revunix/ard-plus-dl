package tui

import (
	"fmt"
	"strings"
)

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
