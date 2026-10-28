package tui

// The key hints of the interface live in one place: the footer shows the
// most important keys of the current screen, the help popup lists all of
// them.

// hintSet bundles the short list for the footer with the full list for the
// help popup.
type hintSet struct {
	short []keyHint
	full  []keyHint
}

// hints returns the key hints of the current screen.
func (m *Model) hints() hintSet {
	switch m.screen {
	case ScreenStart:
		return hintSet{
			short: []keyHint{
				{Key: "tab", Label: "Feld"},
				{Key: "enter", Label: m.startAction()},
				{Key: "esc", Label: "zurück"},
			},
			full: []keyHint{
				{Key: "tab", Label: "Feld wechseln"},
				{Key: "up/down", Label: "Feld wechseln"},
				{Key: "enter", Label: m.startAction()},
				{Key: "esc", Label: "zurück"},
			},
		}

	case ScreenLibrary:
		return hintSet{
			short: []keyHint{
				{Key: "enter", Label: "öffnen"},
				{Key: "d", Label: "laden"},
				{Key: "n", Label: "prüfen"},
				{Key: "tab", Label: "Katalog"},
			},
			full: []keyHint{
				{Key: "enter", Label: "öffnen"},
				{Key: "tab", Label: "Katalog"},
				{Key: "d", Label: "Folgen laden"},
				{Key: "n", Label: "auf Neuheiten prüfen"},
				{Key: "/", Label: "suchen"},
				{Key: "m", Label: "markieren"},
				{Key: "r", Label: "Serie zurücksetzen"},
				{Key: "e", Label: "Serie einstellen"},
				{Key: "y", Label: "URL kopieren"},
				{Key: "space", Label: "aktivieren"},
				{Key: "a", Label: m.libraryAddLabel()},
				{Key: "s", Label: "Einstellungen"},
				{Key: "x", Label: "entfernen"},
				{Key: "q", Label: "beenden"},
			},
		}

	case ScreenCatalog:
		return hintSet{
			short: []keyHint{
				{Key: "enter", Label: "öffnen"},
				{Key: "a", Label: "aufnehmen"},
				{Key: "/", Label: "suchen"},
				{Key: "←/→", Label: "Serien/Filme"},
				{Key: "g", Label: "Genre"},
				{Key: "tab", Label: "Bibliothek"},
			},
			full: []keyHint{
				{Key: "enter", Label: "öffnen und Folgen laden"},
				{Key: "a", Label: "in die Bibliothek"},
				{Key: "/", Label: "Titel oder Genre suchen"},
				{Key: "←/→", Label: "Serien, Filme, Alle"},
				{Key: "1/2/3", Label: "Serien, Filme, Alle"},
				{Key: "g", Label: "nächstes Genre"},
				{Key: "G", Label: "voriges Genre"},
				{Key: "esc", Label: "Filter löschen"},
				{Key: "y", Label: "URL kopieren"},
				{Key: "r", Label: "neu laden"},
				{Key: "tab", Label: "Bibliothek"},
				{Key: "s", Label: "Einstellungen"},
				{Key: "q", Label: "beenden"},
			},
		}

	case ScreenSeries:
		return hintSet{
			short: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "enter", Label: m.seriesAction()},
				{Key: "esc", Label: "zurück"},
			},
			full: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "a", Label: "alle wählen"},
				{Key: "enter", Label: m.seriesAction()},
				{Key: "up/down", Label: "wählen"},
				{Key: "pgup/pgdown", Label: "blättern"},
				{Key: "home/end", Label: "Anfang oder Ende"},
				{Key: "esc", Label: "zurück"},
			},
		}

	case ScreenTatort:
		return hintSet{
			short: []keyHint{
				{Key: "enter", Label: "starten"},
				{Key: "esc", Label: "zurück"},
			},
			full: []keyHint{
				{Key: "enter", Label: "starten"},
				{Key: "esc", Label: "zurück"},
			},
		}

	case ScreenDetails:
		if m.details.Kind == "series" && len(m.detailSeasons) > 0 {
			return hintSet{
				short: []keyHint{
					{Key: "enter", Label: "Folgen"},
					{Key: "d", Label: "laden"},
					{Key: "esc", Label: "Bibliothek"},
				},
				full: []keyHint{
					{Key: "up/down", Label: "Staffel wählen"},
					{Key: "enter", Label: "Folgen öffnen"},
					{Key: "d", Label: "Staffel laden"},
					{Key: "esc", Label: "Bibliothek"},
				},
			}
		}
		return hintSet{
			short: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "d", Label: "laden"},
				{Key: "esc", Label: "Bibliothek"},
			},
			full: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "a", Label: "alle wählen"},
				{Key: "d", Label: "laden"},
				{Key: "y", Label: "Titel kopieren"},
				{Key: "esc", Label: "Bibliothek"},
			},
		}

	case ScreenSeasonEpisodes:
		return hintSet{
			short: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "d", Label: "laden"},
				{Key: "esc", Label: "Staffeln"},
			},
			full: []keyHint{
				{Key: "space", Label: "wählen"},
				{Key: "a", Label: "alle wählen"},
				{Key: "d", Label: "laden"},
				{Key: "y", Label: "Titel kopieren"},
				{Key: "esc", Label: "Staffeln"},
			},
		}

	case ScreenQueue, ScreenLoading:
		return hintSet{
			short: []keyHint{
				{Key: "l", Label: m.logToggleLabel()},
				{Key: "x", Label: "Datei löschen"},
				{Key: "q", Label: "abbrechen"},
			},
			full: []keyHint{
				{Key: "l", Label: "Aktivität ein/aus"},
				{Key: "x", Label: "Datei löschen"},
				{Key: "y", Label: "Pfad kopieren"},
				{Key: "o", Label: "Ordner öffnen"},
				{Key: "q", Label: "abbrechen"},
				{Key: "ctrl+c", Label: "beenden"},
			},
		}

	case ScreenSummary:
		return hintSet{
			short: []keyHint{
				{Key: "l", Label: "Bibliothek"},
				{Key: "q", Label: "beenden"},
			},
			full: []keyHint{
				{Key: "l", Label: "Bibliothek"},
				{Key: "q", Label: "beenden"},
			},
		}

	case ScreenEditEntry:
		return hintSet{
			short: []keyHint{
				{Key: "enter", Label: "speichern"},
				{Key: "esc", Label: "zurück"},
			},
			full: []keyHint{
				{Key: "tab", Label: "Feld wechseln"},
				{Key: "enter", Label: "speichern"},
				{Key: "esc", Label: "zurück"},
			},
		}
	}
	return hintSet{}
}

// startAction names what the start button does.
func (m *Model) startAction() string {
	if m.cfg.URL == "" && len(m.library) > 0 {
		return "speichern"
	}
	return "starten"
}

// seriesAction names what the enter key does on the season screen.
func (m *Model) seriesAction() string {
	if m.categoryMode {
		return "übernehmen"
	}
	return "starten"
}

// libraryAddLabel names what a does in the library: with the catalog it
// leads there, otherwise it asks for an address.
func (m *Model) libraryAddLabel() string {
	if m.hasCatalog() {
		return "Titel aus dem Katalog"
	}
	return "URL aufnehmen"
}
