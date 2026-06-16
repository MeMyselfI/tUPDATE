# tUPDATE

> **⚠️ BETA**: Dieses Tool befindet sich aktuell in der Beta-Phase. Bitte vor jedem Einsatz ein Backup erstellen lassen und das Verhalten in einer Test-Installation prüfen, bevor produktive Server damit angefasst werden. Beim Start erscheint ein lokalisierter Warnhinweis.

Plattformübergreifender CLI-Updater für die Java-basierte tOSCE-Server-Anwendung. Lädt eine ZIP mit der aktuellen Server-Version aus dem Netz oder von einer lokalen Quelle, vergleicht den Inhalt mit der laufenden Installation, fragt vor jeder destruktiven Aktion nach und synchronisiert anschließend die konfigurierten Verzeichnisse. UI-Sprache wird aus der OS-Locale (`LC_ALL` / `LC_MESSAGES` / `LANG`) gezogen — unterstützt Deutsch, Englisch und Französisch.

Geschrieben in Go, ohne CGo, ohne externe Dienstprogramme. Liefert kleine, stripped Single-File-Binaries für Windows, macOS und Linux.

## Inhalt

- [Layout](#layout)
- [Schnellstart](#schnellstart)
- [Konfiguration](#konfiguration)
- [CLI-Flags](#cli-flags)
- [Workflow](#workflow)
- [Sprachunterstützung (i18n)](#sprachunterst%C3%BCtzung-i18n)
- [Verhalten bei Service-Fehlern](#verhalten-bei-service-fehlern)
- [Logging](#logging)
- [Backup](#backup)
- [Exit-Codes](#exit-codes)
- [Build](#build)
- [Tests](#tests)
- [Sicherheit](#sicherheit)
- [Limitierungen](#limitierungen)

## Layout

```
<app-root>/
├── updater/
│   └── updater                   # tUPDATE Binary (Windows: updater.exe)
├── conf/
│   └── updater.properties        # Hauptkonfiguration
├── bin/                          # synchronisiert (Beispiel)
├── www/                          # synchronisiert (Beispiel)
├── etc/                          # synchronisiert (Beispiel)
├── lib/                          # synchronisiert (Beispiel)
├── libs/                         # synchronisiert (Beispiel)
└── backup/                       # automatisch erzeugte Backups
    └── 2026-06-16-17-25-01.zip
```

Die Executable lebt im Unterordner `updater/`. tUPDATE löst die App-Root via `dirname(dirname(executable))` auf und findet `conf/`, die Sync-Verzeichnisse und das Backup-Verzeichnis als Geschwister von `updater/`.

## Schnellstart

```bash
# Aus dem App-Root ausgeführt (Default-Konfig wird gefunden)
./updater/updater
```

Mit lokaler ZIP, ohne Service-Stopp/Start und ohne Rückfragen (z. B. für Automation):

```bash
./updater/updater --zip /pfad/zur/release.zip --skip-service --no-prompt
```

Vorschau ohne Änderungen am Dateisystem:

```bash
./updater/updater --dry-run
```

## Konfiguration

Standardpfad: `<app-root>/conf/updater.properties`. Mit `--config <pfad>` lässt sich eine alternative Datei laden.

Beispiel:

```properties
# === Download ===
download.url = https://cloud.example.org/s/releases/tOSCE-Server-latest.zip
download.timeout.seconds = 300

# === Proxy (leer = direkt) ===
proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy = localhost,127.0.0.1

# === Sync-Verzeichnisse (komma-getrennt, relativ zu App-Root, rekursiv) ===
# Fehlende Dirs (weder im ZIP noch live) werden ohne Fehler übersprungen.
sync.directories = bin, www, etc, lib, libs

# === Service-Kommandos (per Betriebssystem) ===
service.stop.windows  = net stop tOSCEServer
service.stop.darwin   = launchctl stop org.ucan.tosce
service.stop.linux    = systemctl stop tosce
service.start.windows = net start tOSCEServer
service.start.darwin  = launchctl start org.ucan.tosce
service.start.linux   = systemctl start tosce
service.stop.timeout.seconds  = 60
service.start.timeout.seconds = 60

# === Backup ===
backup.directory = backup
```

**Format-Regeln (einfach):**

- Eine Zeile pro Eintrag, Form `key = value`.
- Lines die mit `#` beginnen sind Kommentare; leere Zeilen werden ignoriert.
- Trennzeichen ist das erste `=` — weitere `=` zählen zum Wert (URLs mit Query-Strings OK).
- Key und Wert werden um umgebende Leerzeichen getrimmt.
- Duplikate: letzter Wert gewinnt.
- Keine `\`-Line-Continuation, keine Java-Unicode-Escapes.
- UTF-8 BOM am Anfang wird entfernt.
- Unbekannte Keys werden toleriert (Forward-Kompat).

**Pflicht-Keys**: `download.url`, `download.timeout.seconds`, `sync.directories`, `service.{stop,start}.{windows,darwin,linux}`, `service.stop.timeout.seconds`, `service.start.timeout.seconds`, `backup.directory`.

**Proxy-Verhalten**: Leerer `proxy.url` deaktiviert den Proxy komplett. Bei gesetztem `proxy.url` werden `proxy.user`/`proxy.password` in die URL injiziert. `proxy.no_proxy` ist komma-getrennt; ein Eintrag matcht den Request-Host exakt oder als Suffix (`example.com` matcht auch `sub.example.com`). Ein führender Punkt erzwingt Suffix-Match (`.example.com` matcht nicht `example.com`).

## CLI-Flags

```
updater                           Default-Workflow (Download + Diff + Prompts + Apply)
updater --zip <path>              lokale ZIP statt Download verwenden
updater --config <path>           alternative Properties-Datei
updater --app-root <path>         App-Root explizit setzen (überschreibt dirname-Logik)
updater --dry-run                 nur Diff anzeigen, nichts verändern
updater --no-prompt               keine Rückfragen, Default-Antwort wird genutzt (ja)
updater --skip-service            Service-Stop/-Start auslassen
updater --version                 Version + Build-Info
updater --help                    Hilfe
```

## Workflow

```
┌─ 1  Bootstrap        Properties laden, App-Root via dirname(dirname(exe))
├─ 2  HTTP-Client      Transport mit Proxy + no_proxy
├─ 3  Acquire ZIP      Download ODER --zip
├─ 4  Extract          tmpDir via os.MkdirTemp, defer Cleanup
├─ 5  Service-Stop     OS-Kommando aus Config, Exit-Code-Check
├─ 6  Diff             Size-Pre-Check + xxhash64 lazy
├─ 7  Report           +N ~M -K pro Sync-Dir + Gesamt
├─ 8  Review-Prompt    3-Wege: Weiter [J] / Abbruch [n] / Liste [a]
├─ 9  Backup-Prompt    optional: <root>/backup/<timestamp>.zip
├─ 10 Update-Prompt    Abbruch möglich
├─ 11 Apply            copy/overwrite/delete, leere Parent-Dirs prunen
├─ 12 Service-Start    OS-Kommando aus Config
└─ 13 Cleanup          tmpDir + downloaded ZIP entfernt
```

### Review-Prompt (Phase 8)

Direkt nach der Diff-Zusammenfassung fragt tUPDATE in drei Optionen:

```
Weitermachen, abbrechen oder die vollständige Dateiliste anzeigen? [J/n/a]
Continue, abort, or show the full file list?                       [Y/n/a]
Continuer, annuler ou afficher la liste complète des fichiers ?    [O/n/a]
```

- **J/Y/O** (Default, ENTER): weiter zur Backup-Frage
- **n**: Abbruch mit Exit-Code 7
- **a**: vollständige Liste pro Datei ausgeben — Format: `[+] bin/helper.sh` (neu), `[~] bin/run.sh` (überschrieben), `[-] www/stale.html` (gelöscht). Danach folgt eine y/n-Bestätigung (Default = Ja), bevor der Workflow weiterläuft.

Diese Phase hilft besonders bei verdächtig großen Diffs (z. B. ZIP mit unerwarteter Ordnerstruktur → viele False-Positive-Deletions): die Liste zeigt die genauen Pfade vor jeder destruktiven Aktion.

**Recovery**: Schlägt ein Schritt nach erfolgreichem Service-Stop fehl, wird der Service per Best-Effort wieder gestartet — keine Verwaisung des gestoppten Dienstes.

## Sprachunterstützung (i18n)

Alle Konsolen- und Prompt-Texte sowie der Beta-Warnhinweis werden in der vom Betriebssystem signalisierten Sprache ausgegeben. Erkennung in dieser Reihenfolge:

1. `LC_ALL`
2. `LC_MESSAGES`
3. `LANG`

Die ersten zwei Buchstaben des ersten gesetzten Werts entscheiden:

| Präfix | Sprache | Beispiel-Suffix Prompt |
|--------|---------|------------------------|
| `de`   | Deutsch | `[j/N]`, akzeptiert `j`/`ja`/`nein` |
| `fr`   | Französisch | `[o/N]`, akzeptiert `o`/`oui`/`non` |
| `en` (Default) | Englisch | `[y/N]`, akzeptiert `y`/`yes`/`no` |

Unbekannte oder leere Locales (`C`, `POSIX`, `it`, …) fallen auf Englisch zurück. Yes-Wörter aller Sprachen werden immer akzeptiert (`y/yes/j/ja/o/oui`), nur Prompt-Suffix und Retry-Hinweis sind locale-spezifisch.

**Beispiele:**

```bash
LANG=de_DE.UTF-8 ./updater/updater
LC_ALL=fr_FR.UTF-8 ./updater/updater
LANG=en_US.UTF-8 ./updater/updater
```

## Verhalten bei Service-Fehlern

Wenn ein konfiguriertes `service.stop.*`- oder `service.start.*`-Kommando einen Nicht-Null-Exit-Code liefert, gibt tUPDATE den Fehler aus und fragt zurück:

```
Trotzdem fortfahren? [j/N]
Continue anyway? [y/N]
Continuer malgré tout ? [o/N]
```

- **Bei Ja**: Workflow läuft weiter. Schlug der Stop fehl, gilt der Service als nicht gestoppt — er wird am Ende auch nicht versucht zu starten. Schlug der Start am Ende fehl, wird der Lauf trotz Fehler mit `0` beendet.
- **Bei Nein**: Lauf bricht mit `4` (Stop) bzw. `6` (Start) ab.

Mit `--no-prompt` (Automation) entfällt die Rückfrage — Service-Fehler beenden den Lauf sofort mit dem passenden Exit-Code.

## Logging

Jeder Lauf erzeugt ein Logfile im OS-Temp-Verzeichnis:

- Linux: `/tmp/updater-2026-06-16-17-25-01.log`
- macOS: `/var/folders/.../T/updater-2026-06-16-17-25-01.log`
- Windows: `%TEMP%\updater-2026-06-16-17-25-01.log`

Das Log enthält Start-/End-Marker mit RFC3339-Zeitstempel und alle Konsolen-Ausgaben (stdout + stderr). Der Pfad wird beim Start auf der Konsole ausgegeben.

## Backup

Wenn der User die Rückfrage „Backup erstellen?" bejaht (oder `--no-prompt` Default `Ja` greift), werden alle in `sync.directories` aufgeführten Verzeichnisse vor der Apply-Phase in eine ZIP-Datei gepackt:

```
<app-root>/<backup.directory>/<yyyy-mm-dd-hh-mm-ss>.zip
```

Inhalt:

- Rekursiv alle regulären Dateien aus den Sync-Verzeichnissen.
- Unix-Mode-Bits (`chmod`) bleiben erhalten.
- Symlinks werden übersprungen.

## Exit-Codes

| Code | Bedeutung |
|------|-----------|
| 0 | OK |
| 1 | Config-Fehler (fehlend, Pflichtwert nicht gesetzt, ungültig) |
| 2 | Download fehlgeschlagen |
| 3 | Extract fehlgeschlagen |
| 4 | Service-Stop fehlgeschlagen |
| 5 | Sync fehlgeschlagen (Diff oder Apply) |
| 6 | Service-Start fehlgeschlagen |
| 7 | User-Abbruch via Prompt |

## Build

```bash
# Alle 7 Targets cross-compilen
make build-all

# Einzelnes Target
make dist/updater-linux-amd64

# Stripped + UPX-gepackt (UPX-fähiger Host nötig)
make upx-all

# Windows-Manifest neu erzeugen (für requireAdministrator)
make windows-manifest

# Aufräumen
make clean
```

**Build-Matrix:**

| Target | Datei |
|--------|-------|
| Windows AMD64 | `dist/updater-windows-amd64.exe` |
| Windows ARM64 | `dist/updater-windows-arm64.exe` |
| macOS AMD64 (Intel) | `dist/updater-darwin-amd64` |
| macOS ARM64 (Apple Silicon) | `dist/updater-darwin-arm64` |
| **macOS Universal (lipo)** | `dist/updater-darwin-universal` |
| Linux AMD64 | `dist/updater-linux-amd64` |
| Linux ARM64 | `dist/updater-linux-arm64` |

Build-Flags: `-trimpath -ldflags "-s -w -X main.version=<version>"`. Stripped-Größe: 5.5–6.1 MB pro Single-Arch, ~12 MB für die Mac-Universal.

**Windows-Manifest**: Die generierten `cmd/updater/resource_windows_*.syso`-Dateien sind im Repo eingecheckt. Sie betten ein Manifest mit `requestedExecutionLevel=requireAdministrator` ein, sodass die `.exe` automatisch eine UAC-Erhöhung anfordert. Regeneration über `make windows-manifest`.

**UPX-Caveat (macOS 15 Tahoe)**: Aktuelle Homebrew-UPX-5.2.0-Bottle wird beim Install von AMFI gelöscht. UPX-Packing daher auf Linux-Build-Host oder via signierte UPX-Installation ausführen. UPX wird nur auf Windows- und Linux-Targets angewendet, niemals auf macOS-Binaries (würde Codesigning/Notarisierung brechen).

## Tests

```bash
# Alle Pakete + E2E
make test
# oder direkt
go test ./...
```

Coverage-Status: ~92 Tests, Module-/Integration-Mix, alle grün. E2E-Tests in `cmd/updater/run_test.go` bauen reale ZIPs und führen den Workflow gegen Temp-Trees aus.

## Sicherheit

- **HTTP**: TLS via stdlib; Proxy-Credentials werden nicht geloggt.
- **Zip-Slip**: Eintragspfade aus dem ZIP werden vor dem Extrahieren auf Traversal (`../...`) geprüft und abgelehnt.
- **Pfad-Validierung**: `sync.directories` lehnt absolute Pfade und Parent-Traversal in der Konfiguration ab.
- **Mode-Bits**: Beim Extract und Apply werden Unix-Permissions (z. B. `0755` für Skripte) übernommen.
- **Windows-UAC**: `requireAdministrator` im Manifest ist Pflicht, weil die typischen Service-Kontrolle-Kommandos (`net stop`, `sc.exe`) sonst Permission Denied liefern.

## Limitierungen

- **Symlinks** in Sync-Verzeichnissen werden ignoriert (weder verglichen noch synchronisiert noch gebackupt).
- **Atomicity**: Schlägt `Apply` mittendrin fehl (z. B. weil Java-JAR auf Windows noch gesperrt ist), wird der Sync NICHT automatisch zurückgerollt. Der Backup-Pfad wird im Log + auf der Konsole ausgegeben.
- **Selbst-Update**: tUPDATE aktualisiert NICHT sich selbst.
- **Singleton-Annahme**: Kein Lockfile — paralleler Doppelstart kann zu kaputten Zuständen führen.
- **Tatsächliche Service-Verifikation**: Es wird nur der Exit-Code des `service.stop`-/`service.start`-Kommandos geprüft, kein Port-/Prozess-Healthcheck.

## Lizenz

Proprietär. Alle Rechte vorbehalten.
