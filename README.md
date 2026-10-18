# ard-plus-dl

Hilfswerkzeug für [ARD-Plus-Abonnenten](https://www.ardplus.de/) zur lokalen Archivierung von Filmen, Serien und Tatort-Sammlungen - nur im Rahmen eines gültigen Abos und des geltenden Rechts.

Fork von [chrmei/ard-plus-dl](https://github.com/chrmei/ard-plus-dl), das auf [marco79cgn/ard-plus-dl](https://github.com/marco79cgn/ard-plus-dl) aufbaut - mit Batch-Verarbeitung, GraphQL-Integration, einheitlicher Dateibenennung und verbesserter Fehlerbehandlung.

> **Rechtlicher Hinweis:** Nur für die eigene, private Nutzung mit gültigem ARD-Plus-Abo. Keine Weitergabe heruntergeladener Inhalte. Details: [LEGAL.md](LEGAL.md)

## Features

- Filme, Serien und Tatort-Kategorien werden automatisch erkannt
- Mehrere Tonspuren und Untertitel, sofern verfügbar
- Interaktive Staffelauswahl oder `--automatic` für unbeaufsichtigte Abläufe
- Mehrere URLs aus einer Link-Datei (`--links-file`) - z. B. zum Fortsetzen unterbrochener Jobs
- Dateien werden direkt mit einheitlichem Namensschema gespeichert (`Titel.S01E01.German.AAC.1080p.Web-DL.x264-TAG.mp4`); der eigene Datei-Tag ist Pflichtangabe
- Vorhandene Dateien werden übersprungen, abgebrochene Downloads fortgesetzt (`--force-redownload` verwirft auch Teil-Dateien)

## Voraussetzungen

Bash, [jq](https://jqlang.github.io/jq/), [yt-dlp](https://github.com/yt-dlp/yt-dlp), [ffmpeg](https://ffmpeg.org/) inklusive `ffprobe` (von yt-dlp für `--merge-output-format mp4`, `bv+mergeall` und `--embed-subs` benötigt; `ffprobe` liefert die Medien-Tags für die Dateinamen — bei Installation über pip oft nicht automatisch dabei), `perl` (Tatort-Episodenliste aus `ld+json`), `column` aus [util-linux](https://git.kernel.org/pub/scm/utils/util-linux/util-linux.git/) (Staffeltabelle; nicht auf allen Systemen in coreutils enthalten), GNU-Tools (`curl`, `grep`, `awk`, `cut`, `sed`, `base64`, `tr`) und eine aktive [ARD-Plus-Mitgliedschaft](https://www.ardplus.de/). Im Docker-Image ist alles Benötigte inklusive `ffmpeg` bereits enthalten.

## Installation

```bash
git clone https://github.com/revunix/ard-plus-dl.git
cd ard-plus-dl
chmod 755 ard-plus-dl.sh
```

Nur das Skript:

```bash
curl -O https://raw.githubusercontent.com/revunix/ard-plus-dl/refs/heads/main/ard-plus-dl.sh
chmod 755 ard-plus-dl.sh
```

Das Skript ist eigenständig: Es benötigt keine weiteren Dateien aus dem Repository (die GraphQL-Dokumente sind eingebettet). Eine ältere Kopie, die noch `graphql-queries.sh` neben sich erwartet, bricht beim Start mit einer Meldung über eine fehlende Datei ab — dann das Skript neu laden.

## Verwendung

```bash
./ard-plus-dl.sh [-h|--help]
./ard-plus-dl.sh [--automatic] [--links-file <datei>] [--force-redownload] <url> [<username> <password>] [skip]
```

Optionen müssen vor den Positionsargumenten stehen: Das Skript liest Optionen nur am Anfang ein, das erste Argument ohne führende `-` beginnt die Positionsliste.

| Option / Parameter | Beschreibung |
|--------------------|--------------|
| `-h`, `--help` | Usage ausgeben und mit Exit 0 beenden; eine unbekannte Option meldet `Unknown option: <arg>` auf stderr und endet mit Exit 1 |
| `--automatic` | Alle Staffeln/Episoden ohne Rückfrage verarbeiten |
| `--links-file <datei>` | Mehrere URLs aus Datei (aktiviert `--automatic`) |
| `--force-redownload` | Vorhandene `.mp4`-Dateien und Teil-Downloads des Ziels (`*.part*`, `*.ytdl` und Fragmente) verwerfen und neu laden; yt-dlp erhält zusätzlich `--force-overwrites` |
| `url` | ARD-Plus-Übersichtsseite (Film, Serie oder Tatort-Kategorie). Andere Hosts oder Pfade werden vor der Abhängigkeitsprüfung mit `Invalid URL: <Grund>` auf stderr und Exit 1 abgelehnt (im Batch-Modus landen sie als `invalid URL: …` in `failed_links_*`) |
| `username`, `password` | Optional: eigene ARD-Plus-Zugangsdaten, beide nötig (siehe [Zugangsdaten](#zugangsdaten)) |
| `skip` | Optional, nur Serien — das gilt auch im Batch-Modus (`--links-file`): erste N Episoden der **ersten Staffel** überspringen. Ohne Angabe oder mit `0`/`1`: nichts überspringen. `N` ≥ 2 überspringt die ersten N Episoden der ersten Staffel; weitere Staffeln starten jeweils bei Episode 1. Bei Tatort-Kategorien gilt dieser Parameter nicht (`--automatic`: alle Episoden; interaktiv: separate Abfrage). |

Nach der URL wertet das Skript höchstens drei Positionsargumente aus. Zwei Werte gelten als `username` **und** `password` — beide sind nötig; fehlt eines, endet der Aufruf mit `Credentials missing!`. Ein einzelner Wert ist nur dann `skip`, wenn er rein numerisch ist; ein einzelner Textwert wird als `username` gelesen und führt wegen des fehlenden Passworts ebenfalls zu `Credentials missing!`.

### `skip`-Parameter (Serien)

Der optionale Zähler am Ende der Kommandozeile steuert nur Serien-Downloads; das gilt auch im Batch-Modus (`--links-file`). Intern wird er an `tail -n +…` übergeben; der Standardwert `1` bedeutet daher **kein** Überspringen (nicht „eine Episode überspringen“). Explizit `0` oder `1` hat dieselbe Wirkung. Ein Wert `N` ≥ 2 lässt die ersten N Episoden der ersten verarbeiteten Staffel aus; jede weitere Staffel beginnt wieder bei Episode 1.

### Zugangsdaten

Zugangsdaten werden in dieser Reihenfolge gesucht:

1. **Positionsargumente** `<username> <password>` - **nicht empfohlen**, da Kommandozeilenargumente für alle Prozesse auf dem System sichtbar sind (`ps`, `/proc/<pid>/cmdline`) und in der Shell-History landen.
2. **Umgebungsvariablen** `ARD_PLUS_USER` und `ARD_PLUS_PASSWORD` - empfohlen, auch für Docker (`docker run -e ...` oder `--env-file`).
3. **Zugangsdaten-Datei** `~/.config/ard-plus-dl/credentials` (Pfad via `ARD_PLUS_CREDENTIALS_FILE` änderbar, `chmod 600` empfohlen):

   ```ini
   username=mein-benutzer
   password=mein-passwort
   tag=example
   ```

   Als Schlüssel akzeptiert die Datei auch `ARD_PLUS_USER=`, `ARD_PLUS_PASSWORD=` und `ARD_PLUS_TAG=`.

4. **Interaktive Abfrage** beim Login, falls nichts davon gesetzt ist.

Sobald ein gültiges Session-Token existiert (`~/.local/state/ard-plus-dl/token`, Pfad via `XDG_STATE_HOME` verschiebbar), sind für weitere Aufrufe keine Zugangsdaten mehr nötig.

### Datei-Tag (Pflicht)

Der Datei-Tag ist ein selbst gewählter Namenszusatz, der an jeden Datei- und Ordnernamen angehängt wird (nach dem letzten `-`). Er dient nur der eigenen Kennzeichnung der Sammlung, zum Beispiel `example`. Er wird in dieser Reihenfolge gesucht und muss angegeben werden:

1. **Umgebungsvariable** `ARD_PLUS_TAG`
2. **Zugangsdaten-Datei**: `tag=...` bzw. `ARD_PLUS_TAG=...`
3. **Interaktive Abfrage** beim Start (nur bei TTY; im Batch-Modus nicht)

Erlaubt sind Buchstaben, Ziffern, `.`, `_` und `-`, maximal 16 Zeichen, beginnend mit Buchstabe oder Ziffer. Fehlt die Angabe, endet das Skript mit `Datei-Tag missing!` (Exit 1); ein ungültiger Wert meldet `Invalid Datei-Tag`.

**Beispiele:**

```bash
# Empfohlen: Zugangsdaten und Datei-Tag per Umgebungsvariablen
export ARD_PLUS_USER='user' ARD_PLUS_PASSWORD='pass' ARD_PLUS_TAG='example'

# Serie (interaktiv) / Film / Tatort
./ard-plus-dl.sh 'https://www.ardplus.de/details/a0T0100000064DB-gegen-den-wind'
./ard-plus-dl.sh 'https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt'
./ard-plus-dl.sh 'https://www.ardplus.de/kategorie/tatort-bremen'

# Unbeaufsichtigt (eigene Inhalte, gültiges Abo vorausgesetzt)
./ard-plus-dl.sh --automatic 'https://www.ardplus.de/details/a0T0100000064DB-gegen-den-wind'

# Mehrere URLs - eine pro Zeile, `#`-Kommentare erlaubt (siehe links.txt.example)
./ard-plus-dl.sh --links-file links.txt

# Weiterhin möglich (nicht empfohlen): Zugangsdaten als Argumente
./ard-plus-dl.sh 'https://www.ardplus.de/kategorie/tatort-bremen' user pass
```

Unterbrochene Downloads einfach erneut starten: Fertige Dateien werden übersprungen (`SKIP (already exists)` im Log), unfertige Downloads behalten ihre Teil-Dateien (`*.part*`) und werden fortgesetzt (yt-dlp `--continue`); liegen gebliebene `.ytdl`-Metadatendateien und Fragmente räumt das Skript dabei auf. `--force-redownload` verwirft sowohl die Ausgabedatei als auch die Datei im bisherigen Schema samt Teil-Dateien und lädt neu. Dateien im bisherigen Schema (`Show/Season 01/Show S01E01 - Titel.mp4`) gelten weiter als vorhanden und werden nicht erneut geladen; Umbenennen ist nicht nötig. Protokolle in `logs/` (`download_log_*`, `successful_links_*`, `failed_links_*`) entstehen nur mit `--links-file`; im Einzelmodus geht die gesamte Ausgabe auf stdout. Ein Batch-Lauf endet mit Exit 1, wenn mindestens ein Link fehlgeschlagen ist; die Fehlermeldungen zu fehlender Link-Datei und fehlender URL gehen auf stderr.

Fehler einzelner Episoden brechen die Serie nicht mehr ab: Die betroffene Episode wird als `FEHLER: …` gemeldet, die restlichen Episoden und Staffeln werden weiter geladen, und der Link gilt mit einer Sammelmeldung wie `1 episode download(s) failed (first: …)` als fehlgeschlagen (`failed_links_*`). Im Batch-Modus landet die yt-dlp-Fehlerausgabe (letzte 20 Zeilen) zusätzlich im Download-Log. Laufzeitfehler bei der Wiedergabe-Autorisierung erneuern einmalig die Session (`Session abgelaufen oder ungültig, neuer Login...`), bevor der Download als fehlgeschlagen gilt; dauerhafte GraphQL-Fehler werden nicht mehr wiederholt.

Die interaktive Staffelauswahl wird geprüft: Nur existierende Staffelnummern werden akzeptiert (`1` oder `1,2` bzw. durch Leerzeichen getrennt), ungültige Eingaben führen zu einer erneuten Abfrage, und ein abgebrochener Eingabestrom führt zu `no season selected (input ended)` mit Exit 1. Pathologische Titel werden auf 200 Byte pro Namensbestandteil gekürzt, damit lange Film-, Episoden- und Tatort-Namen auch auf Dateisystemen mit 255-Byte-Grenze (`NAME_MAX`) anlegbar bleiben.

## Unterstützte URLs

| Typ | Beispiel-Pfad | Verhalten |
|-----|---------------|-----------|
| Film | `…/details/a0S01000000EWYi-lola-rennt` | Einzeldatei |
| Serie | `…/details/a0T0100000064DB-gegen-den-wind` | Staffeln/Episoden |
| Tatort | `…/kategorie/tatort-bremen` | Alle Folgen der Stadt |

## Ausgabe

Standard: `downloads/` neben der Link-Datei bzw. im Arbeitsverzeichnis. Anderes Ziel via `DOWNLOADS_DIR` (Docker: `/data/downloads`).

Dateinamen folgen einem einheitlichen Schema: Titel mit Punkten statt Leerzeichen, `SxxEyy`, Medien-Tags aus `ffprobe` und der Datei-Tag am Ende.

| Typ | Beispiel |
|-----|----------|
| Serie | `downloads/Gegen.den.Wind.S01.German.AAC.1080p.Web-DL.x264-example/Gegen.den.Wind.S01E01.German.AAC.1080p.Web-DL.x264-example.mp4` |
| Film | `downloads/Lola.rennt.1998/Lola.rennt.1998.German.AAC.1080p.Web-DL.x264-example.mp4` |
| Tatort | `downloads/Tatort.Köln.Folge.850.2019.German.AAC.1080p.Web-DL.x264-example.mp4` |

Der Download läuft zunächst auf den bisherigen Arbeitsnamen (dort greift auch die Fortsetzung mit `*.part*`); nach dem Merge wird die Datei anhand von `ffprobe` benannt und verschoben. Lässt sich `ffprobe` nicht auswerten, behält die Datei den Arbeitsnamen und das Skript meldet eine Warnung. Ein vorhandener Staffelordner wird wiederverwendet, auch wenn er noch einen älteren Datei-Tag trägt.

Staffeln mit 100 oder mehr Folgen werden dreistellig numeriert (`S21E001` … `S21E151`), kürzere Staffeln zweistellig (`S01E01`). Maßgeblich ist die höchste Episodennummer der Staffel.

## Docker (lokal bauen)

Es wird kein vorgefertigtes Container-Image veröffentlicht. Image lokal bauen und ausführen:

```bash
git clone https://github.com/revunix/ard-plus-dl.git
cd ard-plus-dl
docker build -t ard-plus-dl .
docker run --rm -it -v "$(pwd)/:/data" \
  -e ARD_PLUS_USER='username' -e ARD_PLUS_PASSWORD='password' -e ARD_PLUS_TAG='example' \
  ard-plus-dl 'https://www.ardplus.de/details/a0T01000003LeBR-vorstadtweiber'
```

`ffmpeg` ist im Image enthalten. Der `ENTRYPOINT` ist `/app/ard-plus-dl.sh`, die Argumente werden direkt angehängt; die frühere Form `ard-plus-dl download '<url>'` funktioniert über den Symlink `/usr/bin/download` weiter. Das Arbeitsverzeichnis im Container ist `/data` (entspricht dem gemounteten Host-Verzeichnis); Downloads landen unter `/data/downloads`. Die Session-Token-Datei liegt unter `/data/ard-plus-dl/token` (`XDG_STATE_HOME=/data`), die Zugangsdaten-Datei wird unter `/data/ard-plus-dl/credentials` gesucht (`XDG_CONFIG_HOME=/data`) und ist damit über den Mount erreichbar. Beides bleibt über `--rm`-Läufe hinweg erhalten, solange `/data` gemountet ist.

Alternativ die Variablen in eine Datei schreiben und mit `--env-file` übergeben, damit die Zugangsdaten nicht in der Shell-History landen.

Windows: Host-Pfad mit Backslashes mounten, z. B. `-v C:\Users\Du\Videos:/data`.

## Lizenz & Attribution

Basiert auf [marco79cgn/ard-plus-dl](https://github.com/marco79cgn/ard-plus-dl) und dem Fork [chrmei/ard-plus-dl](https://github.com/chrmei/ard-plus-dl), lizenziert unter [Unlicense](LICENSE). Erweiterungen in diesem Fork unter derselben Lizenz.
