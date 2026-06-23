# tUPDATE

> **⚠️ BETA**: Dieses Tool befindet sich aktuell in der Beta-Phase. Bitte vor jedem Einsatz ein Backup erstellen lassen und das Verhalten in einer Test-Installation prüfen, bevor produktive Server damit angefasst werden. Beim Start erscheint ein lokalisierter Warnhinweis.

Plattformübergreifender, generischer CLI-Updater für server- oder dateibasierte Anwendungen. Lädt eine ZIP mit der aktuellen Version aus dem Netz oder von einer lokalen Quelle, vergleicht den Inhalt mit der laufenden Installation, fragt vor jeder destruktiven Aktion nach und synchronisiert anschließend die konfigurierten Verzeichnisse. UI-Sprache wird aus der OS-Locale (`LC_ALL` / `LC_MESSAGES` / `LANG`) gezogen — unterstützt Deutsch, Englisch und Französisch.

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
- [DB-Backup (pg_dump)](#db-backup-pg_dump)
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
    └── 2026-06-16-17-25-01.tar.xz
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
download.url = https://example.org/releases/app-latest.zip
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
service.stop.windows  = net stop MyService
service.stop.darwin   = launchctl stop org.example.myapp
service.stop.linux    = systemctl stop myservice
service.start.windows = net start MyService
service.start.darwin  = launchctl start org.example.myapp
service.start.linux   = systemctl start myservice
service.stop.timeout.seconds  = 60
service.start.timeout.seconds = 60

# === Backup ===
backup.directory = backup

# === Optionaler DB-Dump (pg_dump) ===
#pgdump.path.windows = C:\Program Files\PostgreSQL\16\bin\pg_dump.exe
#pgdump.path.darwin  = /opt/homebrew/opt/postgresql@16/bin/pg_dump
#pgdump.path.linux   = /usr/bin/pg_dump
#pgdump.host     = localhost
#pgdump.port     = 5432
#pgdump.user     = postgres
#pgdump.password = geheim
#pgdump.db       = mydb
#pgdump.args = --schema=public
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

Vollständige, gruppierte Referenz mit Beispielen liefert das Tool selbst:

```bash
./updater/updater --help
```

Kurzüberblick:

| Flag | Wirkung |
|------|---------|
| `--zip <path>` | lokale ZIP statt Download |
| `--config <path>` | alternative Properties-Datei |
| `--app-root <path>` | App-Root explizit setzen |
| `--dry-run` | nur Diff anzeigen, kein Apply |
| `--no-prompt` | keine Rückfragen (Backup=ja, DB-Backup=ja, Update=ja, Service-Fehler=Abbruch) |
| `--no-files-backup` | Datei-Backup-Schritt (`.tar.xz`) komplett überspringen |
| `--no-db-backup` | DB-Backup-Schritt komplett überspringen |
| `--backup-format <fmt>` | `zip` oder `tar.xz`. Ohne Flag interaktiv gefragt, sonst `tar.xz` |
| `--backup-compression <l>` | `min` / `default` / `max`. Ohne Flag interaktiv gefragt, sonst `default` |
| `--ignore-service-errors` | Service-Stop/-Start-Fehler nur loggen, weiterlaufen |
| `--skip-service` | Service-Stop/-Start gar nicht aufrufen |
| `--logfile <path>` | Logfile-Pfad selbst wählen (Default: `<TempDir>/updater-<ts>.log`) |
| `--detach` | in den Hintergrund forken (setzt `--no-prompt` voraus) |
| `--lang <de\|en\|fr>` | UI-Sprache erzwingen, überschreibt `LC_ALL` / `LC_MESSAGES` / `LANG` |
| `--json` | NDJSON-Events auf stdout statt lokalisierter Ausgabe (setzt `--no-prompt` voraus) |
| `--version` | Version + Build-Info |
| `--help` | gruppierte Hilfe + Beispiele |

### Java / Service-Self-Update via --detach

`--detach` re-execed sich selbst in eine eigene Session (Unix: `setsid`, Windows: `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`). Der re-execete Child hängt nicht mehr am Parent-Terminal / Service-Job-Object, überlebt also den Service-Stop, den er selbst auslöst. Stdin/Stdout/Stderr des Childs werden auf `/dev/null` (Unix) bzw. `nul` (Windows) verdrahtet — die komplette Ausgabe geht ins via `--logfile` festgelegte Logfile.

```java
// Java-Service, der sich selbst per tUPDATE aktualisiert:
new ProcessBuilder(
    "/opt/myapp/updater/updater",
    "--detach",
    "--no-prompt",
    "--logfile", "/var/log/myapp/updater.log",
    "--zip", "/tmp/release.zip",
    "--ignore-service-errors"     // weil wir uns selbst gleich stoppen
).inheritIO().start();
// Sofort returnen, JVM darf der eigene Stop-Befehl gleich beenden.
```

Der Aufruf liefert auf stderr genau eine Zeile zurück:

```
Updater detached, PID=12345, logfile=/var/log/myapp/updater.log
```

Danach exit 0 — die JVM kann gefahrlos sterben. Der Updater läuft als eigenständiger Prozess weiter, stoppt den Service, synct, startet ihn neu, schreibt den kompletten Verlauf ins Logfile.

`--detach` ist nur in Kombination mit `--no-prompt` zulässig; ohne stdin würde der Updater sonst stumm an einem unzugänglichen Prompt hängen.

### Maschinenlesbare Ausgabe (--json)

In `--json`-Modus werden auf stdout und im Logfile **ausschließlich** NDJSON-Events geschrieben (jede Zeile = eines JSON-Objekt). Es gibt **keine** lokalisierten Strings auf stdout/stderr — alle humanen Ausgaben werden in `io.Discard` umgeleitet. `--json` erzwingt `--no-prompt`, weil ohne stdin keine Rückfragen beantwortet werden könnten.

Event-Schema (Auswahl):

| event | wichtige Felder | wann |
|-------|------------------|------|
| `ready` | `version`, `logfile` | beim Start |
| `download_start` / `download_done` / `download_failed` | `url`, `bytes`, `reason` | Download-Phase |
| `extract_done` | `ref_root` | nach ZIP-Entpacken |
| `diff` | `added`, `modified`, `removed`, `per_dir` | nach Diff-Berechnung |
| `backup_files_ok` / `backup_files_failed` | `path`, `bytes`, `reason` | Datei-Backup (`.tar.xz`) |
| `backup_db_ok` / `backup_db_failed` / `backup_db_skipped` | `path`, `bytes`, `reason` | pg_dump |
| `service_stop_ok` / `service_stop_failed` | `reason` | Service-Stop |
| `service_start_ok` / `service_start_failed` | `reason` | Service-Start |
| `apply_ok` / `apply_failed` | `reason` | nach Apply |
| `dry_run_check` | `name`, `ok`, `detail` | jeder einzelne Pre-Flight-Check |
| `detached` | `pid`, `logfile` | --detach-Parent vor Exit |
| `fatal_error` | `stage`, `reason` | Nicht klassifizierter Abbruch |
| `exit` | `code` | Letztes Event, immer vorhanden |

Jedes Event trägt zusätzlich `ts` (RFC3339 UTC).

Beispielausgabe (Java-Aufruf, `--no-files-backup --no-db-backup`):

```json
{"event":"ready","version":"0.7.0","logfile":"/var/log/u.log","ts":"2026-06-17T18:00:00Z"}
{"event":"extract_done","ref_root":"/var/folders/.../updater-extract-123","ts":"..."}
{"event":"diff","added":1,"modified":0,"removed":0,"per_dir":{"bin":{"added":1,"modified":0,"removed":0}},"ts":"..."}
{"event":"apply_ok","ts":"..."}
{"event":"exit","code":0,"ts":"..."}
```

### Pre-Flight-Checks (--dry-run)

`--dry-run` führt eine Reihe nicht-mutativer Checks aus, **bevor** irgendetwas am Dateisystem oder Service angefasst wird.

| Check | Was wird geprüft? | fatal? |
|-------|-------------------|--------|
| `backup_dir_writable` | `<app-root>/<backup.directory>` wird ggf. angelegt, Probe-Datei geschrieben + sofort gelöscht | **ja** |
| `service_stop_binary` | erstes Token aus `service.stop.<os>` (z.B. `launchctl`, `systemctl`, `net`) muss auf PATH sein. Skipped mit `--skip-service` | **ja** |
| `service_start_binary` | dito für `service.start.<os>` | **ja** |
| `pgdump_binary` | `pgdump.path.<os>` ODER `exec.LookPath("pg_dump")`. Skipped mit `--no-db-backup` | **ja** |
| `pgdump_conn_host` | `pgdump.host` ODER `PGHOST`-Env (informational; libpq fällt auf unix socket / localhost zurück) | nein |
| `pgdump_conn_database` | `pgdump.db` ODER `PGDATABASE`-Env | **ja** |
| `pgdump_conn_user` | `pgdump.user` ODER `PGUSER`-Env | **ja** |
| `pgdump_conn_password` | `pgdump.password` ODER `PGPASSWORD`-Env ODER `~/.pgpass` existiert. Der Wert selbst wird **nie** geloggt, nur die Quelle | **ja** |
| `pgdump_connectivity` | `pg_isready` (read-only TCP-Handshake) mit denselben Connection-Parametern. Wird übersprungen wenn `pg_isready` nicht auf PATH ist | nein |
| `download_url` | HTTP-`Range: bytes=0-1023`-GET gegen `download.url`. Body wird nicht gelesen. Skipped mit `--zip <pfad>` | **ja** |

Fatal = Check-Fail → exit 9. Informational = Check-Fail wird trotzdem geloggt, beeinflusst Exit-Code aber nicht (DB kann legitim grad down sein, libpq hat Defaults für Host).

Ausgabe (Human-Modus):

```
Check backup_dir_writable: OK (/opt/myapp/backup)
Check service_stop_binary: OK (/bin/launchctl)
Check service_start_binary: OK (/bin/launchctl)
Check pgdump_binary: OK (/opt/homebrew/bin/pg_dump)
Check pgdump_conn_host: OK (set via pgdump.host)
Check pgdump_conn_database: OK (set via pgdump.db)
Check pgdump_conn_user: OK (set via PGUSER env)
Check pgdump_conn_password: OK (set via /Users/me/.pgpass)
Check pgdump_connectivity: OK (localhost:5432 - accepting connections)
Check download_url: OK (HTTP 206 https://example.org/releases/app-latest.zip)
```

Im `--json`-Modus pro Check ein `{"event":"dry_run_check","name":"...","ok":true|false,"detail":"..."}` mit der Quelle (bei conf-Wert wins → `set via <key>`, bei env wins → `set via <ENV> env`, bei `.pgpass` → `set via <pfad>`). Der Passwort-Wert selbst taucht nirgends auf.

Exit-Codes: **0** = alle fatalen Checks ok, **9** = mindestens ein fataler Check fehlgeschlagen.

Wird `--dry-run` mit `--zip` kombiniert, werden zusätzlich Extract + Diff ausgeführt (kein Service-Stop, kein Apply), damit der Operator „was würde sich ändern?" sieht.

### Vollautomatischer Lauf (CI / Cron)

```bash
# Update einspielen, ohne Backups, Service-Fehler ignorieren, kein Prompt:
updater --no-prompt \
        --zip /tmp/release.zip \
        --no-files-backup \
        --no-db-backup \
        --ignore-service-errors
```

Alle interaktiven Stellen sind via Flag steuerbar. `--ignore-service-errors` ändert nichts an der Konsolen-/Logfile-Ausgabe — die Fehlermeldung wird weiter gedruckt, nur die Rückfrage entfällt und der Lauf bricht nicht ab.

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
├─ 9  Backup-Prompts   2 unabhängige Fragen: Datei-Backup + DB-Backup (pg_dump)
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

Default-Pfad pro OS (wenn `--logfile` nicht gesetzt ist):

- Linux: `/tmp/updater-2026-06-16-17-25-01.log`
- macOS: `/var/folders/.../T/updater-2026-06-16-17-25-01.log`
- Windows: `%TEMP%\updater-2026-06-16-17-25-01.log`

Mit `--logfile <pfad>` lässt sich ein beliebiger Pfad setzen. Relative Pfade werden gegen das aktuelle Arbeitsverzeichnis aufgelöst, fehlende Eltern-Ordner automatisch angelegt. Das Logfile wird bei jedem Lauf **truncated** — Rotation ist Sache des Operators.

Das Log enthält Start-/End-Marker mit RFC3339-Zeitstempel und alle Konsolen-Ausgaben (stdout + stderr). Der Pfad wird beim Start auf der Konsole ausgegeben.

## Backup

Wenn der User die Rückfrage „Backup erstellen?" bejaht (oder `--no-prompt` Default `Ja` greift), werden alle in `sync.directories` aufgeführten Verzeichnisse vor der Apply-Phase in ein Archiv gepackt. **Format und Kompressionsstufe sind wählbar** — interaktiv per Rückfrage oder via `--backup-format` / `--backup-compression` (dann ohne Rückfrage):

```
<app-root>/<backup.directory>/<yyyy-mm-dd-hh-mm-ss>.tar.xz   (Default-Format)
<app-root>/<backup.directory>/<yyyy-mm-dd-hh-mm-ss>.zip      (--backup-format zip)
```

**Format:**

| Format | Beschreibung |
|--------|--------------|
| `tar.xz` (Default) | tar + xz/LZMA2 (dieselbe Engine wie 7-Zip). Kleiner, einkernig. Restore: `tar -xJf`, 7-Zip, xz. |
| `zip` | klassisches DEFLATE-ZIP. Größer, aber schneller und überall nativ entpackbar. |

**Kompressionsstufe** (gilt für beide Formate):

| Stufe | tar.xz (LZMA2) | zip (DEFLATE) | Charakter |
|-------|----------------|---------------|-----------|
| `min` | 1 MiB Dict, HashTable | BestSpeed | schnell, größer |
| `default` (dt. „Standard") | 8 MiB Dict, HashTable | Default | ausgewogen |
| `max` | 64 MiB Dict, BinaryTree (`xz -9`-Klasse) | BestCompression | kleinste Datei, **langsam, ~0,7 GB RAM** |

Ohne Flag/Prompt (z. B. `--no-prompt`): **`tar.xz` / `default`**.

Inhalt:

- Rekursiv alle regulären Dateien aus den Sync-Verzeichnissen.
- Unix-Mode-Bits (`chmod`) bleiben erhalten.
- Symlinks werden übersprungen.
- Das Backup-Verzeichnis selbst wird beim Packen übersprungen (kein Selbst-Einschluss, auch wenn es in einem Sync-Verzeichnis liegt).

Hinweise:

- **Entpacken** (manueller Restore): tUPDATE spielt Backups nicht selbst zurück, sondern gibt nur den Pfad aus.
- **Live-Fortschritt**: Auf einem interaktiven Terminal zeigt die Backup-Phase eine `\r`-Prozentanzeige. Unter `--json`, `--detach` oder umgeleitetem stderr (`--logfile`) wird sie unterdrückt.
- **`max` ist auf großen Verzeichnissen sehr langsam** (einkernig); für Server-Backups vor einem Update meist `default` oder `zip`/`min` sinnvoller.

## DB-Backup (pg_dump)

Der DB-Backup-Prompt wird **immer** gestellt — unabhängig von der Antwort auf den Datei-Backup-Prompt. Bei "Ja" versucht tUPDATE einen `pg_dump`-Lauf.

```
<app-root>/<backup.directory>/<yyyy-MM-dd-HH-mm-ss>-db.backup
```

- Format: `-Fc` (PostgreSQL Custom Format, komprimiert, mit `pg_restore` einspielbar)
- Hartkodierter Timeout: 30 Minuten
- Identischer Timestamp wie das Datei-Backup (falls auch erstellt) → Paare bilden direkt zusammen
- Stdout + Stderr des `pg_dump`-Prozesses werden ins Logfile gespiegelt
- `pgdump.args` (optional) wird hinter `-Fc -f <out>` angehängt (`strings.Fields`-Splitting, keine Quoting-Unterstützung)

**Connection-Parameter (3 Wege, kombinierbar):**

| Conf-Key            | wird gesetzt als | Hinweis |
|---------------------|------------------|---------|
| `pgdump.host`       | `PGHOST`         |         |
| `pgdump.port`       | `PGPORT`         | String, keine Validierung |
| `pgdump.user`       | `PGUSER`         |         |
| `pgdump.password`   | `PGPASSWORD`     | Klartext → Datei `chmod 600` schützen; **löst `-w`-Auto-Inject aus** |
| `pgdump.db`         | `PGDATABASE`     |         |

Reihenfolge (last wins): Parent-Process-Env → Conf-Keys → `pgdump.args` (CLI-Flags überschreiben Env). Wer kein Klartext-Passwort in der Properties-Datei haben möchte, lässt `pgdump.password` weg und benutzt `~/.pgpass` (`chmod 600`) oder eine vorher gesetzte `PGPASSWORD`-Env-Variable.

**Auto-`-w`**: Sobald `pgdump.password` gesetzt ist, schiebt tUPDATE `-w` (= „nie nach Passwort fragen") direkt hinter `-Fc -f <out>` — sonst würde `pg_dump` interaktiv blockieren und in den Timeout laufen. Wer `-w` schon selbst in `pgdump.args` mitgibt, bekommt es nicht doppelt.

**Binary-Lookup-Reihenfolge:**

1. `pgdump.path.<aktuelles-OS>` aus der Properties-Datei
2. Fallback: `exec.LookPath("pg_dump")` (Standard-PATH-Suche)
3. Wenn beides leer → Info-Zeile auf der Konsole + im Logfile, Workflow läuft weiter

**Fehlerverhalten:** Schlägt `pg_dump` fehl (Exit-Code ≠ 0 oder Timeout), gibt tUPDATE die Fehlermeldung aus und macht weiter. Es gibt keinen "Continue anyway"-Prompt und keinen dedizierten Exit-Code. Die teilweise geschriebene `.backup`-Datei wird gelöscht, damit kein 0-Byte-Müll liegen bleibt.

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
| 9 | `--dry-run`-Pre-Flight-Check fehlgeschlagen (backup-dir / pg_dump / download.url) |

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
