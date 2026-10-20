package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// splashView shows the logo while the app starts.
func (m *Model) splashView() []string {
	width := m.frameWidth()
	logo := gradientText("ard-plus-dl")
	lines := []string{theme.Title.Render(logo)}
	lines = append(lines, theme.Muted.Render("ARD Plus Downloader"))
	lines = append(lines, theme.Divider.Render(strings.Repeat("─", 24)))
	if m.version != "" {
		lines = append(lines, theme.Muted.Render("Version "+m.version))
	}
	lines = append(lines, m.spinnerView()+" "+theme.Muted.Render("Bibliothek wird geladen…"))
	return m.frameRows(nil, centerBlock(width, m.height, lines), nil)
}

// centerBlock centers lines in the middle of the screen and fills the rest.
func centerBlock(width, height int, lines []string) []string {
	if width <= 0 {
		return lines
	}
	block := make([]string, 0, len(lines))
	for _, line := range lines {
		block = append(block, centerLine(line, width))
	}
	if height <= 0 {
		return block
	}
	top := max(0, (height-len(block))/2)
	out := make([]string, 0, height)
	for range top {
		out = append(out, "")
	}
	out = append(out, block...)
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

// editEntryView shows the settings of a single series: the file tag and the
// number of episodes to skip.
func (m *Model) editEntryView() []string {
	width := m.frameWidth()
	header := m.header("Serie", entryTitle(m.editEntry))

	labels := []string{"Datei-Tag", "Überspringen"}
	rows := make([]string, 0, len(labels))
	for index, field := range []textinput.Model{m.editTag, m.editSkip} {
		marker := "  "
		labelStyle := theme.Muted
		if index == m.editFocus {
			marker = theme.Cursor.Render("▸ ")
			labelStyle = theme.Accent.Bold(true)
		}
		rows = append(rows, marker+labelStyle.Render(padRight(labels[index], 14))+" "+field.View())
	}
	rows = append(rows, "")
	rows = append(rows, theme.Muted.Render("Der Datei-Tag steht am Ende jedes Dateinamens. 0 oder 1 überspringt keine Folge."))

	footer := m.screenFooter(width)

	var errorLines []string
	if m.editErr != "" {
		errorLines = wrapText(m.editErr, m.flowWidth(width))
	}
	card := renderCard(width, "Einstellungen", fitInner(rows, m.cardTextWidth(width)), true, "")
	body := card
	if len(errorLines) > 0 {
		body = append(body, "")
		for index, line := range errorLines {
			text := line
			if index == 0 {
				text = theme.Danger.Render("● ") + line
			}
			body = append(body, fitLine(theme.Danger.Render(text), width))
		}
	}
	return m.frameRows(header, body, footer)
}

// helpPopup returns the window with all keys of the current screen. It
// floats over the page instead of replacing it.
func (m *Model) helpPopup() []string {
	hints := m.hints().full
	if len(hints) == 0 {
		hints = []keyHint{{Key: "?", Label: "Hilfe"}}
	}
	entries := make([]string, 0, len(hints))
	for _, hint := range hints {
		entries = append(entries, renderKeycap(hint.Key)+" "+theme.KeyLabel.Render(hint.Label))
	}
	entries = append(entries, renderKeycap("ctrl+c")+" "+theme.KeyLabel.Render("beenden"))

	columns := 1
	columnWidth := 34
	if m.frameWidth() >= 76 {
		// Two columns keep the window short.
		columns = 2
		columnWidth = 32
	}
	var lines []string
	if columns == 2 {
		half := (len(entries) + 1) / 2
		left := entries[:half]
		right := entries[half:]
		for len(right) < len(left) {
			right = append(right, "")
		}
		lines = strings.Split(joinColumns(left, right, columnWidth, columnWidth), "\n")
	} else {
		lines = entries
	}
	// The window has to fit on the screen.
	if m.height > 0 && len(lines)+4 > m.height-2 {
		lines = lines[:max(1, m.height-6)]
		lines = append(lines, theme.Muted.Render("… weitere Tasten im Hilfe-Fenster"))
	}
	return renderPopup(min(2*columnWidth+6, m.frameWidth()), "Tasten", lines)
}

func (m *Model) startView() []string {
	width := m.frameWidth()
	title := "Start"
	subtitle := "Zugang und Download-Ziel"
	cardTitle := "Zugang"
	hint := "Zugangsdaten werden lokal gespeichert und nur für die Anmeldung bei ARD Plus verwendet."
	switch m.formPurpose {
	case formSettings:
		subtitle = "Einstellungen"
		cardTitle = "Einstellungen"
		hint = "Der Datei-Tag steht am Ende jedes Dateinamens und muss gesetzt sein."
	case formAddURL:
		subtitle = "Neue URL hinzufügen"
		cardTitle = "Neue Serie"
		hint = "Die URL muss auf eine Serie, einen Film oder eine Tatort-Stadt zeigen."
	}
	header := m.header(title, subtitle)

	labels := []string{"URL", "Benutzer", "Passwort", "Datei-Tag"}
	rows := make([]string, 0, len(m.fields))
	for index, field := range m.fields {
		marker := "  "
		labelStyle := theme.Muted
		if index == m.focus {
			marker = theme.Cursor.Render("▸ ")
			labelStyle = theme.Accent.Bold(true)
		}
		row := marker + labelStyle.Render(padRight(labels[index], 12)) + " " + field.View()
		rows = append(rows, row)
	}
	if m.showsThemeField() {
		marker := "  "
		labelStyle := theme.Muted
		if m.focus == m.themeFieldIndex() {
			marker = theme.Cursor.Render("▸ ")
			labelStyle = theme.Accent.Bold(true)
		}
		rows = append(rows, marker+labelStyle.Render(padRight("Erscheinungsbild", 16))+" "+theme.Badge.Render(themeLabel(themeMode)))
	}
	rows = append(rows, "")
	rows = append(rows, theme.Muted.Render(hint))

	hints := m.hints().short
	if m.showsThemeField() {
		hints = append(hints, keyHint{Key: "h/d/t", Label: "Erscheinungsbild"})
	}
	footer := m.footerWithToast(width, append(hints, keyHint{Key: "?", Label: "Hilfe"}))

	var errorLines []string
	if m.startErr != nil {
		errorLines = wrapText(m.startErr.Error(), m.flowWidth(width))
	}

	panel := renderCard(width, cardTitle, fitInner(rows, m.cardTextWidth(width)), true, "")
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
			// The card no longer fits beside the error: keep the four fields
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
		for index, line := range errorLines {
			text := line
			if index == 0 {
				text = theme.Danger.Render("● ") + line
			}
			body = append(body, fitLine(theme.Danger.Render(text), width))
		}
	}
	return m.frameRows(header, body, footer)
}

func (m *Model) seriesView() []string {
	width := m.frameWidth()
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
	header := m.header(heading, fmt.Sprintf("%d ausgewählt", selected))

	panelTitle := "Staffeln"
	if m.categoryMode {
		panelTitle = "Kategorien"
	}
	footer := m.screenFooter(width)

	inner := m.cardTextWidth(width)
	budget := listBudget(m.height, header, footer, 0, width)
	budget = min(budget, m.seriesVisible())
	// The hint about an empty selection needs a line of its own.
	hintLines := 0
	if m.selectionErr != "" {
		hintLines = len(wrapText(m.selectionErr, m.flowWidth(width)))
		budget = max(1, budget-hintLines)
	}
	var rows []string
	if len(m.seasons) == 0 {
		rows = clampLines(renderEmptyAction(inner, "Keine Staffeln verfügbar.", "Bitte später erneut prüfen.", "esc", "zurück"), budget)
	} else {
		start, end, top, bottom := windowRange(len(m.seasons), m.cursor, m.seriesOffset, budget)
		if top {
			rows = append(rows, theme.Muted.Render(fmt.Sprintf("  … %d weitere", start)))
		}
		for index := start; index < end; index++ {
			season := m.seasons[index]
			cursor := "  "
			if index == m.cursor {
				cursor = theme.Cursor.Render("▸ ")
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
		for _, line := range wrapText(m.selectionErr, m.flowWidth(width)) {
			rows = append(rows, fitLine(theme.Danger.Render(line), inner))
		}
	}
	panel := renderCard(width, panelTitle, rows, true, "")
	return m.frameRows(header, panel, footer)
}

func (m *Model) tatortView() []string {
	width := m.frameWidth()
	title := truncate("Tatort "+m.tatortCity, max(8, width-2))
	header := m.header(title, "Episoden überspringen")
	footer := m.screenFooter(width)

	inner := m.cardTextWidth(width)
	question := fmt.Sprintf("Wie viele Episoden überspringen? (0 bis %d, 0 = alle laden)", m.tatortCount)
	questionLines := wrapText(question, m.flowWidth(width))
	fieldLine := fitLine(m.skipField.View(), inner)

	if m.skipErr == "" {
		content := append(fitInner(questionLines, inner), "")
		content = append(content, fieldLine)
		return m.frameRows(header, renderCard(width, "Überspringen", content, true, ""), footer)
	}

	available := listBudget(m.height, header, footer, 0, width)
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
	for _, line := range tailWithEllipsis(wrapText(m.skipErr, m.flowWidth(width)), errorBudget) {
		content = append(content, fitLine(theme.Danger.Render(line), inner))
	}
	return m.frameRows(header, renderCard(width, "Überspringen", content, true, ""), footer)
}

func (m *Model) summaryView() []string {
	width := m.frameWidth()
	title := "Fertig"
	if m.doneErr != nil || m.cancelled {
		title = "Abgebrochen"
	}
	subtitle := "Zusammenfassung"
	header := m.header(title, subtitle)
	footer := m.screenFooter(width)

	inner := m.cardTextWidth(width)
	symbol := theme.Success.Render("✓")
	message := []string{"Alle Downloads abgeschlossen."}
	messageStyle := theme.Success
	if m.doneErr != nil {
		symbol = theme.Danger.Render("!")
		message = wrapText(m.doneErr.Error(), m.flowWidth(width))
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
		available = m.height - len(header) - len(footer) - cardOverhead(width)
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
	panel := renderCard(width, "Ergebnis", rows, true, "")
	return m.frameRows(header, panel, footer)
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
	style := theme.Muted
	marker := theme.Accent.Render("●")
	if dangerText(m.detailMessage) {
		style = theme.Danger
		marker = theme.Danger.Render("●")
	}
	wrapped := wrapText(m.detailMessage, max(1, m.flowWidth(width)-2))
	message := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			message = append(message, marker+" "+style.Render(line))
			continue
		}
		message = append(message, style.Render(line))
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
