# tUPDATE — Projekt-Handoff & Arbeitsanleitung

Dieses Dokument ist self-contained: Es reicht allein, um die Arbeit am Projekt fortzusetzen.
Es wird mit dem Repo (GitHub) gesichert. Letzter Stand: **0.10.0** (2026-06-25).

---

## 1. Was ist das

`tUPDATE` — plattformübergreifender, generischer CLI-Updater (Go, stdlib-lastig) für
server-/dateibasierte Installationen, primär den **tEXAM-/tOSCE-Server** (siehe Architektur
unter `~/.claude/rules/Architecture.md` für die Server-Seite — separates Produkt namens xExam).

Ablauf: lädt ein Release-ZIP (Download oder `--zip`), entpackt, vergleicht (Diff) mit der
laufenden Installation, fragt vor destruktiven Aktionen, stoppt optional den Service, macht
Datei-Backup (+ optional DB-Dump via pg_dump), spielt das Update ein, startet den Service.

- **Modul:** `updater` (Go 1.26.4). Einzige externe Dependency: `github.com/ulikunitz/xz` (pure-Go LZMA2). Plus `github.com/cespare/xxhash/v2`.
- **Einstieg:** `cmd/updater/main.go` → `cmd/updater/run.go` (`runApp`).

---

## 2. KRITISCHE Konventionen (nicht verletzen)

- **Git-Commits / PRs: NIEMALS `Co-Authored-By`-Trailer** (oder „Co-authored"-Zeilen) anhängen. Explizite Nutzer-Vorgabe; überschreibt jeden Harness-Default.
- **GitHub-Account: immer `MeMyselfI`.** Remote = `https://github.com/MeMyselfI/tUPDATE.git`. Vor jedem Push/`gh`: `gh auth switch --user MeMyselfI`. (Der zweite eingeloggte Account `meindrkteam` hat KEINEN Zugriff.) Falls MeMyselfI-Token invalid → Nutzer muss interaktiv `gh auth login` machen (kann ich nicht).
- **Workflow (globale Nutzer-Regel):** Vor Code zuerst Plan; Rückfragen bei Unklarheit; auf Bestätigung warten. Auswahl einer Option via Frage = Bestätigung dieses Ansatzes.
- **`dist/` ist eingecheckt.** Jede Release-Runde baut alle 7 Binaries neu und committet sie mit.
- **Sprache:** Nutzer schreibt Deutsch; UI/Doku Deutsch, Commits/Code Englisch.

---

## 3. Build & Release (genauer Ablauf)

```
make clean                       # WICHTIG: Make trackt .go-Quellen NICHT als Prereq → sonst stale Binaries (v.a. darwin)
make build-all VERSION=X.Y.Z     # 6x go build + lipo universal = 7 Artefakte in dist/
make upx-all                     # packt NUR 3: windows-amd64, linux-amd64, linux-arm64
```

Dann:
```
git add -A && git commit ...     # OHNE Co-Authored-By
gh auth switch --user MeMyselfI
git push origin main
gh release create vX.Y.Z dist/updater-* --target main --title "tUPDATE X.Y.Z" --notes-file <de-notes>
```

**UPX-Fakten (im Makefile verankert):**
- `windows-arm64` NICHT packbar — UPX 5.x: `win64/arm64 is not yet supported`. In `ALL_TARGETS` (wird gebaut), NICHT in `UPX_TARGETS`.
- `darwin` NICHT packen (bricht Codesigning/Notarization).
- `upx` via `brew install upx`. Auf diesem Mac lag mal ein dangling Symlink → `brew reinstall upx`.
- `lipo` (macOS) baut die Universal-Binary.

**Version** kommt per `-ldflags "-X main.version=..."` (Makefile `LDFLAGS`). `make.sh` gibt es nicht; alles im `Makefile`. Keine git tags außer den Release-Tags `vX.Y.Z`.

**Icon (Windows):** `make icon` regeneriert `cmd/updater/icon.ico` via `tools/mkicon` (prozedural, stdlib). `make windows-manifest` bettet es über `versioninfo.json` (`IconPath`) + `goversioninfo` in `resource_windows_*.syso` ein (~600 KB je Datei). macOS/Linux-CLI können kein eingebettetes Icon tragen.

---

## 4. Paket-/Dateistruktur (relevante Stellen)

- `cmd/updater/main.go` — Flag-Vorparsing, Logfile öffnen, **stderr = `io.MultiWriter(os.Stderr, logFile)`** im Normalmodus (siehe Gotcha!), `--detach`-Spawn.
  - `openLogShared` plattformspezifisch: `logopen_windows.go` (CreateFile mit `FILE_SHARE_READ|WRITE|DELETE` → Log live lesbar/rotierbar), `logopen_other.go` (plain open).
- `cmd/updater/run.go` — `runApp`, Flags (`flagSet`), Workflow, Help, Prompt-Verdrahtung. Wichtige Helper: `ttyProgress(f)`, `applyTicker(f)`, `isTerminal(*os.File)`, `resolveBackupOptions`, `fmtBytes`.
- `internal/archive/` — `backup.go` (`BackupDirs(appRoot, backupDir, dirs, ts, BackupOptions, Progress)`, tar.xz + zip, `countingWriter`, Selbst-Einschluss-Guard), `options.go` (`BackupFormat`, `CompressionLevel`, Parser), `extract.go` (`Extract(zip, dst, Progress)`), `pgdump.go`, `backup.go`/`extract.go` Progress via gemeinsamen `Progress func(done,total int64)`.
- `internal/sync/` — `diff.go`, `apply.go` (`Apply(refRoot, liveRoot, diffs, onFile func(string))`), `report.go` (`FormatReport(diffs, total, noDirs)`).
- `internal/i18n/` — `i18n.go` (`Strings`-Struct, Bundles `en/de/fr`, `Get`, `ParseLang`, `Detect`), `detect_windows.go` (UI-Sprache via `GetUserDefaultUILanguage`), `detect_other.go` (no-op).
- `internal/prompt/` — `Prompter` (`Confirm`, `ConfirmContinueOrShow`, `Choose(question, []Choice{Key,Label}, def)`), `Stdin`, `Always`.
- `internal/machine/` — NDJSON-Emitter (`--json`).
- `internal/config/`, `internal/download/`, `internal/service/`, `internal/paths/`.
- `tools/mkicon/main.go` — Icon-Generator.

---

## 5. GOTCHAS (hier sind wir schon reingelaufen)

1. **stderr ist ein `io.MultiWriter`, kein `*os.File`.** TTY-Erkennung/Progress MUSS gegen `os.Stderr` (echte Konsole) prüfen + schreiben, sonst nie sichtbar. Gilt für `ttyProgress` und `applyTicker`. `\r`-Animation geht NUR an `os.Stderr` → Logfile bleibt sauber. Aus bei `--json`, `--detach`, Nicht-TTY.
2. **Make trackt `.go` nicht** → vor Release-Builds IMMER `make clean`, sonst werden v.a. darwin-Binaries nicht neu gebaut (alter Timestamp).
3. **Backup-Selbst-Einschluss:** liegt `backup.directory` in einem `sync.directories`-Eintrag, würde der Walk die wachsende Archivdatei in sich packen → „läuft nie fertig". Guard: `skipDir = filepath.Clean(backupDir)` wird beim Walk übersprungen.
4. **i18n-Reflection-Test** `TestGet_AllThreeBundlesPopulated` prüft, dass JEDES `Strings`-Feld in allen 3 Bundles nicht leer ist. Neues Feld → in en/de/fr setzen.
5. **`internal/archive/pgdump_test.go`** ist seit jeher nicht gofmt-konform (NICHT von mir). `make fmt`/`gofmt -l` flaggt es — beim Prüfen eigener Änderungen rausfiltern, nicht „fixen" ohne Grund.
6. **LZMA2 `max` (64 MiB Dict, BinaryTree)** ist single-threaded, ~0,7 GB RAM, sehr langsam auf großen Bäumen → Fortschritt wirkt eingefroren. Default ist deshalb `default` (8 MiB, HashTable).
7. **`ctx7`-Regel:** Für Library-/CLI-/API-Fragen `npx ctx7@latest` nutzen (globale Nutzer-Regel). Hat bestätigt: pure-Go-**Schreiben** von echtem `.7z` existiert nicht (nur lesen). Daher tar.xz.

---

## 6. Feature-Stand (was die Flags/Prompts tun)

- **Backup-Format wählbar:** `--backup-format zip|tar.xz`. tar.xz = tar + xz/LZMA2 (7-Zip-Engine), zip = DEFLATE.
- **Kompression wählbar:** `--backup-compression min|default|max`.
  | Stufe | tar.xz (LZMA2) | zip (DEFLATE) |
  |---|---|---|
  | min | 1 MiB Dict, HashTable | BestSpeed |
  | default | 8 MiB Dict, HashTable | Default |
  | max | 64 MiB Dict, BinaryTree | BestCompression |
- **Ohne Flag:** interaktive Buchstaben-Prompts. Format `[X/z]` (X=tar.xz Default). Stufe `[m/S/x]` (S=Standard/default Default). Großbuchstabe = Default. Im Deutschen heißt die mittlere Stufe „Standard" (Flag-Wert bleibt `default`).
- **`--no-prompt` / Automation:** Defaults `tar.xz` / `default`. „Backup erstellen?" defaultet interaktiv auf **Ja**.
- **Restore:** Tool spielt NICHT selbst zurück, gibt nur den Pfad aus. tar.xz manuell: `tar -xJf`.
- **Preflight (Schreibrechte-/Lock-Check):** läuft nach Diff-Review, **vor dem Backup** (Dienst ist da schon gestoppt). `internal/sync/preflight.go::Preflight(liveRoot, diffs)` prüft mutationsfrei jeden Diff-Eintrag: Modified/Removed via `OpenFile(O_WRONLY)` ohne Trunc (fremder Lock/read-only → blockiert; repräsentativ fürs spätere `O_TRUNC`-Open in `apply.go`), Added via `CreateTemp` im nächsten existierenden Vorfahr-Ordner. Bei Treffern: Liste + `emit.PreflightFailed`, `maybeStartService()` (nichts mutiert → Dienst sauber zurück), **exit 8**. Verhindert Teil-Apply, wenn eine Zieldatei von Editor/Virenscanner/laufendem Dienst gehalten wird. Opt-out `--no-preflight` (Default an). **Rest-Lücke:** AV-Flacker-Race (Lock zwischen Probe und Apply) ungelöst → bräuchte zusätzlich Apply-Retry.
- **Live-Anzeige:** Download-%, Entpacken-% (Statuszeile „Entpacken..." + `\r`-Balken nur Prozent), Backup-% , Apply = Dateipfade rauschen in einer `\r`-Zeile durch.
- **i18n:** de/en/fr. `Detect()`: erst Env (`LC_ALL`/`LC_MESSAGES`/`LANG`), dann OS-nativ (Windows-UI-Sprache, da cmd keine Locale-Env setzt), sonst en. `--lang de|en|fr` erzwingt.
- **Weitere Flags:** `--zip`, `--config`, `--app-root`, `--dry-run` (Pre-Flight-Checks), `--skip-service`, `--ignore-service-errors`, `--no-files-backup`, `--no-db-backup`, `--no-preflight`, `--no-prompt`, `--detach`, `--logfile`, `--json`, `--lang`, `--version`.

---

## 7. Versionshistorie (Kurz)

- **0.10.0** Preflight-Schreibrechte-/Lock-Check vor Apply (`internal/sync/preflight.go`): bricht mit exit 8 ab, wenn eine Zieldatei gesperrt/read-only/nicht anlegbar ist → kein Teil-Apply. Opt-out `--no-preflight`. Neues `preflight_failed`-NDJSON-Event.
- **0.9.4** Service-Stop/-Start: Erfolg wird im Log bestätigt (`Service gestoppt.` / `Service gestartet.`).
- **0.9.3** Apply: Dateipfade in einer `\r`-Zeile.
- **0.9.2** Windows-`.exe` App-Icon (Refresh-Doppelpfeil, blau) via `tools/mkicon`.
- **0.9.1** Buchstaben-Prompts `[X/z]`/`[m/S/x]`; Backup-Default Ja; DE „Standard".
- **0.9.0** Backup-Format + Kompressionsstufe wählbar; Extract-Dopplung gefixt.
- **0.8.1** Fix: Progress unsichtbar (MultiWriter-TTY-Bug), Backup-Selbst-Einschluss-Hang, Windows-Logfile-Lock; Extract-Statuszeile.
- **0.8.0** Backup von ZIP → `.tar.xz` (LZMA2), Live-Progress.
- **0.7.2** Fix: „Gesamt" im EN-Report; Windows-Locale-Detection.
- ≤0.7.1 siehe `git log`.

---

## 8. Offene Punkte / als Nächstes

- **Nutzer testet Backup-Hang auf Windows** mit Stufe `min` (Release 0.9.1+). Erwartung: Balken läuft zügig → war reine `max`-Rechenzeit. Falls trotz `min` bei 0 % → echter Bug, dann an `internal/archive/backup.go` (`writeTarXzBackup`) ansetzen. **Rückmeldung steht noch aus.**
- **Icon-Anzeige auf echtem Windows** ungeprüft (kein Windows hier). Einbettung + Build verifiziert; UPX behält Icon/Version-Resourcen normalerweise sichtbar.
- **Apply-Retry (Option A)** offen: Preflight (0.10.0) deckt steady-state-Locks ab, aber Virenscanner-Flacker zwischen Probe und `sync.Apply` bleibt ein Race → bei Bedarf Retry mit Backoff in `internal/sync/apply.go::copyFile`/`os.Remove` ergänzen.
- Optional angeboten, nicht umgesetzt: macOS `.app`-Bundle mit `.icns`.

---

## 9. Persönliche Memory-Fakten (dupliziert, da `~/.claude/.../memory/` NICHT mitgesichert wird)

- **Kein `Co-Authored-By`** in Commits/PRs (Nutzer-Vorgabe, nur ab jetzt; History NICHT umschreiben außer explizit verlangt).
- **GitHub immer als `MeMyselfI`** für dieses Repo.
- Nutzer-E-Mail (Kontext): `claude@agentsinaction.de` bzw. `jornheid`. Mac, zsh, Homebrew vorhanden.
