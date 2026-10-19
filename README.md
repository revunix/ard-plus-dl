# ard-plus-dl

Hilfswerkzeug für [ARD-Plus-Abonnenten](https://www.ardplus.de/) zur lokalen Archivierung von Filmen, Serien und Tatort-Sammlungen - nur im Rahmen eines gültigen Abos und des geltenden Rechts.

Ein einzelnes Go-Binary für Linux, Windows und macOS mit interaktiver Terminal-Oberfläche (TUI) und identischer Kommandozeile für Automatisierung. Der Downloader spricht DASH selbst (kein yt-dlp); ffmpeg und ffprobe werden nur zum Muxen und Auslesen der Medieninformationen verwendet.

> **Rechtlicher Hinweis:** Nur für die eigene, private Nutzung mit gültigem ARD-Plus-Abo. Keine Weitergabe heruntergeladener Inhalte. Details: [LEGAL.md](LEGAL.md)

## Features

- Filme, Serien und Tatort-Kategorien werden automatisch erkannt
- TUI für die interaktive Nutzung: URL, Zugangsdaten, Staffelauswahl, Fortschritt der laufenden Downloads
- Headless-Modus für Automatisierung (`--automatic`, `--links-file`, `--force-redownload`)
- Nativer DASH-Downloader mit Segment-Resume, Wiederholversuchen und mehreren Tonspuren
- Untertitel in Deutsch und Englisch, sofern verfügbar
- Einheitliche Dateinamen über ffprobe-Tags, eigener Datei-Tag ist Pflichtangabe
- Vorhandene Dateien werden übersprungen, abgebrochene Downloads fortgesetzt (`--force-redownload` verwirft auch Teil-Downloads)
- Mehrere URLs aus einer Link-Datei - z. B. zum Fortsetzen unterbrochener Jobs
- **Bibliothek**: beliebig viele Serien/Tatort-Kategorien speichern (SQLite) und später erneut prüfen
- **Neue Folgen erkennen**: `--check` bzw. `n` in der TUI vergleicht die aktuelle Episodenliste mit der Historie
- Zugangsdaten und Datei-Tag werden gemerkt; das Passwort liegt im Schlüsselbund des Systems

## Screenshots

| Downloads | Bibliothek |
|-----------|------------|
| ![Downloads und Aktivität](docs/screenshots/downloads.png) | ![Bibliothek](docs/screenshots/library.png) |

| Staffeln und Folgen |
|---------------------|
| ![Staffeln und Folgen](docs/screenshots/episodes.png) |

## Voraussetzungen

- **ffmpeg und ffprobe** auf dem `PATH` (`ARD_PLUS_FFMPEG` / `ARD_PLUS_FFPROBE` überschreiben die Pfade). Fehlen sie, meldet das Programm `Missing required dependencies: ...`.
- Aktive [ARD-Plus-Mitgliedschaft](https://www.ardplus.de/)

## Installation

Fertige Binaries gibt es auf der [Releases-Seite](https://github.com/revunix/ard-plus-dl/releases) für Linux, macOS und Windows (amd64/arm64). Alternativ:

```bash
go install github.com/revunix/ard-plus-dl/cmd/ard-plus-dl@latest
```

Aus dem Quellbaum:

```bash
git clone https://github.com/revunix/ard-plus-dl.git
cd ard-plus-dl
go build -trimpath -o ard-plus-dl ./cmd/ard-plus-dl
```

## Verwendung

```bash
ard-plus-dl [-h|--help] [-v|--version]
ard-plus-dl [--automatic] [--links-file <datei>] [--force-redownload] <url> [<username> <password>] [skip]
```

Ohne URL startet die TUI und fragt die fehlenden Angaben ab. Optionen müssen vor den Positionsargumenten stehen: Das erste Argument ohne führende `-` beginnt die Positionsliste.

| Option / Parameter | Beschreibung |
|--------------------|--------------|
| `-h`, `--help` | Usage ausgeben und mit Exit 0 beenden; eine unbekannte Option meldet `Unknown option: <arg>` auf stderr und endet mit Exit 1 |
| `-v`, `--version` | Version ausgeben und mit Exit 0 beenden |
| `--automatic` | Alle Staffeln/Episoden ohne Rückfrage verarbeiten |
| `--links-file <datei>` | Mehrere URLs aus Datei (aktiviert `--automatic`) |
| `--force-redownload` | Vorhandene Dateien und Teil-Downloads des Ziels verwerfen und neu laden |
| `url` | ARD-Plus-Übersichtsseite: Film/Serie (`/details/...`), Tatort-Kategorie (`/kategorie/tatort-<stadt>` bzw. kurz `/tatort-<stadt>`) oder das Tatort-Archiv `/tatort`. Andere Hosts oder Pfade werden mit `Invalid URL: <Grund>` und Exit 1 abgelehnt |
| `username`, `password` | Optional: eigene ARD-Plus-Zugangsdaten, beide nötig (siehe [Zugangsdaten](#zugangsdaten)) |
| `skip` | Optional: erste N Episoden der **ersten Staffel** überspringen; `0`/`1` überspringt nichts |
| `--library` | Gespeicherte Serien mit Anzahl neuer Folgen auflisten |
| `--check` | Alle gespeicherten Serien auf neue Folgen prüfen (ohne Download) |
| `--download-new` | Prüfen und die neuen Folgen herunterladen |
| `--add <url>` | Eine Serie/Tatort-Kategorie in die Bibliothek aufnehmen, ohne zu laden |

Jeder normale Download trägt die URL automatisch in die Bibliothek ein.

### TUI

Mit einem Terminal (und ohne `--automatic`/`--links-file`) startet die interaktive Oberfläche auf der **Bibliothek**, sobald Serien gespeichert sind. Die TUI benötigt mindestens 40x12 Zeichen. Lange Listen scrollen; ober- und unterhalb des sichtbaren Ausschnitts zeigt `… N weitere` die Anzahl der verborgenen Zeilen.

`?` blendet die Hilfe ein und aus; jede andere Taste schließt sie wieder. `ctrl+c` hat Vorrang und beendet die TUI auch bei offener Hilfe.

1. **Bibliothek** - gespeicherte Serien/Filme/Tatort-Städte mit Anzahl neuer Folgen
   - `enter` öffnet den Eintrag und aktualisiert Staffeln/Folgen; `d` lädt neue Folgen und öffnet die Download-Ansicht, `n` prüft alle Einträge
   - `space` aktivieren/deaktivieren, `x` entfernt nach erneutem Druck (zweistufige Sicherheitsabfrage in der Statuszeile)
   - `pgup`/`pgdown` blättern, `home`/`end` springen an den Anfang bzw. ans Ende
   - `a` neue URL aufnehmen, `s` Einstellungen (Benutzer, Passwort, Datei-Tag), `q` beenden
2. **Seriendetails** - Staffeln mit Folgenzahl und offenen Folgen
   - `up`/`down` wählen eine Staffel, `pgup`/`pgdown` blättern, `home`/`end` springen an den Anfang bzw. ans Ende
   - `enter` öffnet die Folgenliste, `d` lädt alle offenen Folgen der Staffel
3. **Folgenliste** - Folgen der ausgewählten Staffel mit Status (geladen/offen)
   - `space` wählt einzelne Folgen, `a` alle/keine, `enter`/`d` lädt die Auswahl. Ohne jede Markierung werden alle offenen Folgen der Staffel geladen; nach einer Abwahl werden nur die markierten geladen, bei keiner Markierung keine
   - `pgup`/`pgdown` blättern, `home`/`end` springen an den Anfang bzw. ans Ende
   - `esc` zurück zu den Staffeln; bei Tatort bleibt die Folgenliste flach und `esc` kehrt zur Bibliothek zurück
4. **Start** - URL, Zugangsdaten und Datei-Tag eingeben (`tab` wechselt das Feld, `enter` startet bzw. speichert, `esc` zurück)
5. **Serie** - beim Start einer neuen Serie Staffeln auswählen (`space` umschalten, `a` alle, `enter` starten, `pgup`/`pgdown`/`home`/`end` navigieren)
6. **Tatort** - Anzahl zu überspringender Episoden (`0` bis `N`, `0` = alle laden)
7. **Downloads** - öffnet sich automatisch; links aktive Datei und Fortschritt, rechts die letzten Meldungen; bei schmalen Terminals untereinander. `q`/`ctrl+c`: erster Druck bricht ab, zweiter beendet die TUI
8. **Fertig** - Zusammenfassung; `l` zurück zur Bibliothek, `q`/`enter` beendet

### Tatort-Archiv

`https://www.ardplus.de/tatort` wird auf die ARD-Plus-Sammlung `Tatort Städte` (`/kategorie/tatort-cities`) abgebildet. Die TUI listet die darin verlinkten Stadt-Kategorien wie Köln, Münster und Wien; Ermittler-Team-Sammlungen werden nicht mit eingemischt. `space` wählt einzelne Städte aus, `a` alle. Ohne Terminal bzw. mit `--automatic` wird jede Stadt-Kategorie separat verarbeitet.

Das Archiv selbst wird **nicht** als ein Eintrag gespeichert: Beim Aufnehmen oder Prüfen wird es in einzelne Stadt-Einträge mit der kanonischen URL `/kategorie/tatort-<slug>` zerlegt. Diese erscheinen in der Bibliothek und lassen sich wie jede andere Serie öffnen, prüfen und laden. Ein bereits vorhandener Eintrag `Tatort (alle)` wird beim nächsten `--check` automatisch in Stadt-Kategorien aufgeteilt.

### Bibliothek und neue Folgen

Alle Zustandsdaten liegen in einer SQLite-Datei (Standard: `~/.local/state/ard-plus-dl/library.db`, via `ARD_PLUS_LIBRARY` änderbar):

| Tabelle | Inhalt |
|---------|--------|
| `settings` | Benutzername, Datei-Tag, letzte URL |
| `series` | die Bibliothek: URL, Titel, Art (Serie/Tatort/Film), aktiv, neue Folgen, letzte Prüfung |
| `episodes` | jede bekannte Folge mit Erstfund und Download-Zeitpunkt |

Ein Eintrag gilt als **neu**, wenn seine Episoden-ID beim Prüfen zum ersten Mal auftaucht. Geladene Folgen werden markiert und nicht erneut geladen. Für Zeitsteuerung (z. B. cron) genügt:

```bash
ard-plus-dl --check          # Exit 1, wenn eine Prüfung fehlschlägt
ard-plus-dl --download-new   # lädt nur die neuen Folgen
```

Das **Passwort** wird bevorzugt im Schlüsselbund des Systems abgelegt (macOS Keychain, Windows Credential Manager, Linux Secret Service). Ist keiner erreichbar - etwa im Container -, fällt das Tool auf die Zugangsdaten-Datei mit `chmod 600` zurück und sagt das beim Speichern.

### Zugangsdaten

Zugangsdaten werden in dieser Reihenfolge gesucht:

1. **Positionsargumente** `<username> <password>` - **nicht empfohlen**, da sie für alle Prozesse sichtbar sind (`ps`) und in der Shell-History landen
2. **Umgebungsvariablen** `ARD_PLUS_USER` und `ARD_PLUS_PASSWORD` - empfohlen, auch für Docker
3. **Zugangsdaten-Datei** `~/.config/ard-plus-dl/credentials` (Pfad via `ARD_PLUS_CREDENTIALS_FILE` änderbar, `chmod 600` empfohlen):

   ```ini
   username=mein-benutzer
   password=mein-passwort
   tag=example
   ```

   Als Schlüssel akzeptiert die Datei auch `ARD_PLUS_USER=`, `ARD_PLUS_PASSWORD=` und `ARD_PLUS_TAG=`.
4. **Interaktive Abfrage** im Start-Bildschirm der TUI bzw. am Terminal, falls nichts davon gesetzt ist

Sobald ein gültiges Session-Token existiert (`~/.local/state/ard-plus-dl/token`, via `XDG_STATE_HOME` verschiebbar), sind für weitere Aufrufe keine Zugangsdaten mehr nötig.

### Dateinamen

Der eigene Datei-Tag (`ARD_PLUS_TAG`, `tag=...` oder TUI-Eingabe) ist Pflicht und steht in jedem Dateinamen am Ende. Der Medienteil kommt aus ffprobe:

```
Film:   Title.Year/Title.Year.German.AAC.1080p.Web-DL.x264-<tag>.mp4
Serie:  Show.S01/Show.S01E01.German.AAC.1080p.Web-DL.x264-<tag>.mp4
Tatort: Tatort.Bremen.Folge.1200.2024.German.AAC.1080p.Web-DL.x264-<tag>.mp4
```

Staffelverzeichnisse werden wiederverwendet: Existiert bereits ein Verzeichnis `Show.S01.*`, landet die neue Folge dort. Staffeln mit 100 oder mehr Episoden werden dreistellig nummeriert (`S21E001`).

### Diagnose

Bei CDN-Fehlern (`HTTP 403`) schreibt `ARD_PLUS_DEBUG_DIR` die Rohdaten des letzten Laufs mit:

```bash
ARD_PLUS_DEBUG_DIR="$PWD/debug" ard-plus-dl 'https://www.ardplus.de/details/...'
```

- `manifest.mpd` - die geladene DASH-Beschreibung
- `tracks.txt` - was das Manifest anbietet und was ausgewählt wurde
- `requests.log` - jede Medien-Anfrage mit Statuszeile; Query-Werte sind durch `…` ersetzt, Tokens landen nicht im Log

### Batch-Modus

`--links-file <datei>` liest eine URL pro Zeile (`#` beginnt einen Kommentar) und schreibt Protokolle nach `<links-dir>/logs/`:

- `successful_links_<zeitstempel>.txt`
- `failed_links_<zeitstempel>.txt`
- `download_log_<zeitstempel>.txt`

Der Exit-Code ist 1, sobald ein Link fehlschlägt. Vorlage: [links.txt.example](links.txt.example)

## Docker

```bash
docker build -t ard-plus-dl .
docker run --rm -it \
  -e ARD_PLUS_USER=mein-benutzer \
  -e ARD_PLUS_PASSWORD=mein-passwort \
  -e ARD_PLUS_TAG=example \
  -v "$PWD/downloads:/data/downloads" \
  -v "$PWD/ard-plus-config:/data/ard-plus-dl" \
  ghcr.io/revunix/ard-plus-dl:latest 'https://www.ardplus.de/details/...'
```

Im Image sind `ffmpeg` und `ffprobe` (Alpine-Paket) enthalten. Fertige Multi-Arch-Images liegen unter `ghcr.io/revunix/ard-plus-dl`.

## Entwicklung

```bash
go vet ./...
go test ./... -count=1
```

Die End-to-End-Prüfung `TestEndToEndNativeDash` erzeugt mit ffmpeg einen echten DASH-Stream, lädt ihn über einen lokalen HTTP-Server und prüft Dateiname und Stream-Inventar des Ergebnisses.

Release-Artefakte für alle sechs Ziele (Linux/macOS/Windows × amd64/arm64) baut der GitHub-Actions-Workflow. Ein Tag-Push mit `v*` baut alle sechs Ziele und veröffentlicht sie mit den Archiven und `checksums.txt` unter [github.com/revunix/ard-plus-dl/releases](https://github.com/revunix/ard-plus-dl/releases):

```bash
git tag v1.2.3
git push origin v1.2.3
```

Die Version wird als `main.version` eingebettet und ist über `ard-plus-dl --version` sichtbar.

Aufbau: `internal/media` (Benennung, ffprobe-Tags), `internal/config` (CLI, Zugangsdaten, Pfade), `internal/auth` (Login, Token), `internal/ardplus` (GraphQL, Wiedergabe-Autorisierung), `internal/dash` (MPD, Auswahl, Segmente, Muxing), `internal/download` (Skip/Finalisierung), `internal/app` (Abläufe), `internal/tui` (Bubble Tea), `internal/cli` (headless Einstieg).

## Lizenz

[Unlicense](LICENSE) - basiert auf [marco79cgn/ard-plus-dl](https://github.com/marco79cgn/ard-plus-dl) und dem Fork [chrmei/ard-plus-dl](https://github.com/chrmei/ard-plus-dl).
