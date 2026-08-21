# tUPDATE — Projekt-Handoff & Arbeitsanleitung

Dieses Dokument ist self-contained: Es reicht allein, um die Arbeit am Projekt fortzusetzen.
Es wird mit dem Repo (GitHub) gesichert. Letzter Stand: **0.12.0** (2026-08-21).

---

## 1. Was ist das

`tUPDATE` — plattformübergreifender, generischer CLI-Updater (Go, stdlib-lastig) für
server-/dateibasierte Installationen, primär den **tEXAM-/tOSCE-Server** (siehe Architektur
unter `~/.claude/rules/Architecture.md` für die Server-Seite — separates Produkt namens xExam).

Ablauf: lädt ein Release-ZIP (Download oder `--zip`), entpackt, vergleicht (Diff) mit der
laufenden Installation, fragt vor destruktiven Aktionen, stoppt optional den Service, macht
Datei-Backup (+ optional DB-Dump via pg_dump), spielt das Update ein, startet den Service.

- **Modul:** `updater` (Go 1.26.4). Einzige externe Dependency: `github.com/ulikunitz/xz` (pure-Go LZMA2). (`github.com/cespare/xxhash/v2` ist seit 0.11.0 raus — der Diff hasht nicht mehr, siehe unten.)
- **Einstieg:** `cmd/updater/main.go` → `cmd/updater/run.go` (`runApp`).
- **Lizenz:** Apache-2.0 (`LICENSE` = kanonischer Volltext, `NOTICE` = Copyright + Third-Party-Hinweise). Copyright 2026 Jörn Heid. Kommerz-/Proprietär-Nutzung erlaubt, Patent-Grant inklusive. Per-Datei-SPDX-Header bewusst NICHT gesetzt (Build-Tag-`//go:build`-Zeilen müssen ganz oben stehen → fragil); LICENSE/NOTICE decken das Repo ab.

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
make sign                        # signiert windows-amd64/-arm64 (NACH upx! SimplySign Desktop eingeloggt)
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

**Code Signing (Windows Authenticode):** `make sign` signiert `dist/updater-windows-amd64.exe` + `-arm64.exe` mit dem **Certum Open Source Code Signing**-Zert. Läuft `tools/sign-windows.sh`.
- **Reihenfolge zwingend:** `make sign` **NACH** `make upx-all` — UPX schreibt die `.exe` neu und würde eine vorhandene Signatur strippen. darwin/linux sind nicht Authenticode-signierbar (bewusst ausgeschlossen).
- **Privater Schlüssel liegt in Certums SimplySign-Cloud-HSM** (`never extractable`). Zugriff via PKCS#11: SimplySign Desktop muss **laufen + eingeloggt** sein (Token-ID + OTP aus der SimplySign-Handy-App), sonst ist der Token unsichtbar und das Target bricht mit klarer Meldung ab.
- **Toolchain (Homebrew):** `osslsigncode`, `libp11` (liefert PKCS#11-Engine `engines-3/pkcs11.dylib`), `opensc` (`pkcs11-tool` für die dynamische ID-Abfrage). Plus die **SimplySign Desktop**-App (liefert `/usr/local/lib/libSimplySignPKCS.dylib`).
- **Cert-ID dynamisch:** Das Script liest die PKCS#11-Objekt-ID des Keys zur Laufzeit via `pkcs11-tool -O` → **jährliche Zert-Erneuerung braucht KEINE Änderung**. (Zert selbst 1 Jahr gültig, z. B. 2026-07-31 → 2027-07-31.)
- **Timestamp:** RFC3161 via `http://time.certum.pl` (sha256-Signatur, sha384-TSA) → Signatur überlebt Cert-Ablauf.
- **Idempotent:** bereits signierte `.exe` wird übersprungen (Override: `SIGN_FORCE=1`).
- **Zerts liegen zentral in `~/.certum/`** (seit 2026-08-18, geteilt mit dem meinDRK-CLI-Repo, damit die jährliche Zert-Erneuerung nur an EINER Stelle passiert; Default `SIGN_DIR=$HOME/.certum`, Rückfall auf ein repo-lokales `signing/`): Leaf-Cert (`leaf.pem`, kam von Certum als `.cer`/`.pem`), Intermediate (`inter.pem`, von `repository.certum.pl/ccsca2021.cer`), Root (`root.pem` = CTNCA2), und die **`chain.pem` = leaf+intermediate** (das `-certs`-Bundle fürs Signieren). Kein Private Key auf Platte — der bleibt in der Cloud.
- **KETTEN-GOTCHA (0.10.1 war deswegen kaputt):** `-pkcs11cert` **schlägt `-certs`** — osslsigncode nimmt das Signaturzert dann vom Token und ignoriert das Chain-File komplett. Ergebnis: nur das Leaf im Signaturblock, Windows kann die Kette nicht bauen → UAC zeigt **„Unbekannter Herausgeber"** trotz gültiger Signatur (Intermediate müsste per AIA von `repository.certum.pl/ccsca2021.cer` nachgeladen werden — timeout-/firewall-anfällig, UAC wartet nicht). Fix: zusätzlich **`-ac signing/inter.pem`** („additional certificates to be added to the signature block"). Das Script hat seit 0.10.2 einen Post-Sign-Guard, der die Zerts im Block zählt und bei `<2` abbricht.
- **Überschreibbare Vars:** `SIGN_ENGINE`, `SIGN_MODULE`, `SIGN_CHAIN`, `SIGN_AC`, `SIGN_TS`, `SIGN_HASH`, `SIGN_FORCE`.
- **Verify-Gotcha:** `osslsigncode verify` **ohne `-CAfile`** liefert auf macOS exit≠0 („unable to get local issuer") weil der lokale Trust-Store die Certum-Roots nicht kennt — **kein Signatur-Fehler**. Deshalb greppt das Script den `verify`-**Output** statt sich auf den Exit-Code zu verlassen (sonst killt `set -o pipefail` den Erfolgsfall).
- **Echter Ketten-Test (das, was Windows macht):** `osslsigncode verify -CAfile signing/root.pem -TSA-CAfile signing/root.pem dist/updater-windows-amd64.exe` → muss `Signature verification: ok` sagen. Nur den Root vorgeben, NICHT das Intermediate — sonst testet man die Lücke weg, die Windows sieht.
- **`PKCS7_dataFinal failed` / `Failed to sign spcIndirectDataContent`:** SimplySign-Cloud-Session abgelaufen. `pkcs11-tool -O` listet die Objekte weiterhin (Token „sichtbar"), nur die private-Key-Operation scheitert — die Token-Preflight im Script fängt das also NICHT. Abhilfe: in SimplySign Desktop neu einloggen (Token-ID + OTP).

**Release-Flow mit Signatur:** `make clean` → `make build-all VERSION=X.Y.Z` → `make upx-all` → **`make sign`** → commit → `gh release create`.

---

## 4. Paket-/Dateistruktur (relevante Stellen)

- `cmd/updater/main.go` — Flag-Vorparsing, Logfile öffnen, **stderr = `io.MultiWriter(os.Stderr, logFile)`** im Normalmodus (siehe Gotcha!), `--detach`-Spawn.
  - `openLogShared` plattformspezifisch: `logopen_windows.go` (CreateFile mit `FILE_SHARE_READ|WRITE|DELETE` → Log live lesbar/rotierbar), `logopen_other.go` (plain open).
- `cmd/updater/run.go` — `runApp`, Flags (`flagSet`), Workflow, Help, Prompt-Verdrahtung. Wichtige Helper: `ttyProgress(f)`, `applyTicker(f)`, `diffTicker(f)`, `isTerminal(*os.File)`, `resolveBackupOptions`, `resolveDownloadParts`, `resolveDiffWorkers`, `serviceRunState`, `fmtBytes`.
- `internal/archive/` — `backup.go` (`BackupDirs(appRoot, backupDir, dirs, ts, BackupOptions, Progress)`, `collectEntries` löst Sync-Dirs + `BackupOptions.Include` in eine deduplizierte `[]backupEntry` auf, danach tar.xz **oder** zip über dieselbe Liste; `countingWriter`, Selbst-Einschluss-Guard), `options.go` (`BackupFormat`, `CompressionLevel`, `Include`, Parser), `extract.go` (`Extract(zip, dst, Progress)`), `pgdump.go`, `backup.go`/`extract.go` Progress via gemeinsamen `Progress func(done,total int64)`.
- `internal/sync/` — `diff.go` (`Compute(refRoot, liveRoot, dirs, Options{Progress, Workers})`, `DiffDir`, Worker-Pool, `sameContent` als Block-Vergleich mit Early-Exit), `apply.go` (`Apply(refRoot, liveRoot, diffs, onFile func(string))`), `report.go` (`FormatReport(diffs, total, noDirs)`), `preflight.go`, `conffiles.go` (`CompareConfFiles`/`ApplyConfFiles`/`PreflightConfFiles`/`ConfUpdateCount` für `conf.files`).
- `internal/i18n/` — `i18n.go` (`Strings`-Struct, Bundles `en/de/fr`, `Get`, `ParseLang`, `Detect`), `detect_windows.go` (UI-Sprache via `GetUserDefaultUILanguage`), `detect_other.go` (no-op).
- `internal/prompt/` — `Prompter` (`Confirm`, `ConfirmContinueOrShow`, `Choose(question, []Choice{Key,Label}, def)`), `Stdin`, `Always`.
- `internal/machine/` — NDJSON-Emitter (`--json`).
- `internal/download/` — `client.go` (`NewClient(timeout, ProxyConfig, ClientOptions{Parts})`), `download.go` (Range-Probe, Single-Stream, paralleler Range-Download, Part-Retry), `progress.go` (Ticker-basierte Statuszeile mit Rate + ETA).
- `internal/config/`, `internal/service/`, `internal/paths/`.
- `tools/mkicon/main.go` — Icon-Generator.

---

## 5. GOTCHAS (hier sind wir schon reingelaufen)

1. **stderr ist ein `io.MultiWriter`, kein `*os.File`.** TTY-Erkennung/Progress MUSS gegen `os.Stderr` (echte Konsole) prüfen + schreiben, sonst nie sichtbar. Gilt für `ttyProgress`, `applyTicker` und `diffTicker`. `\r`-Animation geht NUR an `os.Stderr` → Logfile bleibt sauber. Aus bei `--json`, `--detach`, Nicht-TTY.
   **Ausnahme (Altlast, unverändert):** Der *Download*-Progress hängt an `Downloader.Progress = stderr` (also am MultiWriter) und schreibt seine `\r`-Zeilen deshalb auch ins Logfile. War vor 0.11.0 schon so; bewusst nicht mitgeändert.
2. **Make trackt `.go` nicht** → vor Release-Builds IMMER `make clean`, sonst werden v.a. darwin-Binaries nicht neu gebaut (alter Timestamp).
3. **Backup-Selbst-Einschluss:** liegt `backup.directory` in einem `sync.directories`-Eintrag, würde der Walk die wachsende Archivdatei in sich packen → „läuft nie fertig". Guard: `skipDir = filepath.Clean(backupDir)` wird beim Walk übersprungen.
4. **i18n-Reflection-Test** `TestGet_AllThreeBundlesPopulated` prüft, dass JEDES `Strings`-Feld in allen 3 Bundles nicht leer ist. Neues Feld → in en/de/fr setzen.
5. **`internal/archive/pgdump_test.go`** ist seit jeher nicht gofmt-konform (NICHT von mir). `make fmt`/`gofmt -l` flaggt es — beim Prüfen eigener Änderungen rausfiltern, nicht „fixen" ohne Grund.
6. **LZMA2 `max` (64 MiB Dict, BinaryTree)** ist single-threaded, ~0,7 GB RAM, sehr langsam auf großen Bäumen → Fortschritt wirkt eingefroren. Default ist deshalb `default` (8 MiB, HashTable).
7. **Diff-Vergleich hasht NICHT mehr** (seit 0.11.0). `sameContent` liest beide Dateien in 256-KiB-Blöcken lockstep und bricht beim ersten Unterschied ab (`bytes.Equal`). Kein xxhash mehr → Dependency raus. Wer hier „optimieren" will: ein mtime-Shortcut ist **verboten** — reproducible builds setzen feste/identische Zeitstempel, dann würde eine geänderte Datei gleicher Größe als unverändert durchrutschen.
8. **`internal/sync` heißt `sync`** → die stdlib wird dort als `gosync "sync"` importiert. Nicht „aufräumen".
9. **Properties-Parser kennt KEINE Fortsetzungszeilen.** `internal/config/properties.go` liest strikt eine Zeile = ein Eintrag; ein `\` am Zeilenende ist einfach Teil des Werts. Die lange tEXAM-`conf.files`-Liste muss deshalb einzeilig bleiben (steht so auch als Kommentar in `conf/updater.properties`).
10. **`BackupDirs` schreibt Bodies exakt in Stat-Grösse.** Seit 0.12.0 werden Entries vorab gesammelt (`collectEntries`), Header also aus einem früheren `Stat`. `tar.Writer` bricht ab, wenn der Body kürzer ODER länger ist als der Header sagt — deshalb `io.CopyN` + Zero-Padding in `copyIntoArchive`. Nicht „vereinfachen" zurück auf `io.Copy`.
11. **`ctx7`-Regel:** Für Library-/CLI-/API-Fragen `npx ctx7@latest` nutzen (globale Nutzer-Regel). Hat bestätigt: pure-Go-**Schreiben** von echtem `.7z` existiert nicht (nur lesen). Daher tar.xz.

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
- **Paralleler Download (0.11.0):** `--download-parts N` / `download.parallel.parts` (Default **4**, Max 16, `1` = aus). Ablauf in `internal/download`: Probe-GET mit `Range: bytes=0-0` → `206` + parsebares `Content-Range` ⇒ Server kann Ranges, Gesamtgröße bekannt; sonst Single-Stream (bei `200` wird der Probe-Body **wiederverwendet**, kein zweiter Request). Datei wird vorallokiert (`Truncate`), N Goroutines schreiben per `io.NewOffsetWriter` an ihren Offset. Pro Teil 3 Versuche mit Backoff, ein Retry setzt am erreichten Offset auf. Erster Fehler cancelt die anderen Teile.
  - **Split nur ab 8 MiB pro Teil** (`minPartSize`), sonst kostet der Verbindungsaufbau mehr als er bringt.
  - **HTTP/2 wird bei `Parts > 1` bewusst abgewählt** (`TLSClientConfig.NextProtos = ["http/1.1"]`): h2 multiplext alle „parallelen" Teile auf EINE TCP-Verbindung → Drosselung pro Verbindung und h2-Flow-Control würden den Gewinn auffressen. Bei `Parts == 1` bleibt `ForceAttemptHTTP2` an.
  - Weiter: 1-MiB-Copy-Buffer statt 32 KiB, `ReadBufferSize` 256 KiB, **`f.Sync()` entfernt** (es ist eine Tempdatei, die sofort entpackt und gelöscht wird — fsync über hunderte MB war reine Wartezeit).
  - **Erwartung:** 2–4× wenn der Server pro Verbindung drosselt oder die Leitung hohe Latenz hat. Limitiert der Server-Uplink, bringt es **nichts** — dafür zeigt die Statuszeile jetzt MB/s + ETA, damit man das unterscheiden kann. Loopback-Messung 250 MB: 1 Teil 0,81 s → 4 Teile 0,47 s.
- **Schnellerer Diff (0.11.0):** Worker-Pool, `--diff-workers N` / `diff.workers` (Default `min(NumCPU, 8)`). Beide Bäume (ref + live) werden **gleichzeitig** gewalkt. `sameContent` vergleicht blockweise mit Early-Exit statt beide Dateien komplett zu hashen. Messung (2000 Dateien × 128 KiB, warm, M-Serie, Unterschiede absichtlich im *letzten* Byte = Worst Case fürs Early-Exit): alt 139 ms → neu seriell 104 ms → neu mit 8 Workern **48 ms** (~2,9×). Auf Netzlaufwerken/HDD ggf. `diff.workers = 1..2`.
- **Live-Anzeige:** Download-% + **MB/s + ETA**, Entpacken-% (Statuszeile „Entpacken..." + `\r`-Balken nur Prozent), **Diff** (`diffTicker`: Listing-Phase = laufender Dateizähler, Vergleichsphase = echte Prozent pro sync-Verzeichnis), Backup-%, Apply = Dateipfade rauschen in einer `\r`-Zeile durch.
- **i18n:** de/en/fr. `Detect()`: erst Env (`LC_ALL`/`LC_MESSAGES`/`LANG`), dann OS-nativ (Windows-UI-Sprache, da cmd keine Locale-Env setzt), sonst en. `--lang de|en|fr` erzwingt.
- **Service wird nur gestartet, wenn tUPDATE ihn gestoppt hat (0.11.0).** Vor dem Stop fragt `serviceRunState(goos, stopCmd)` den Manager (launchctl/systemctl/net→sc) nach dem Ist-Zustand — Tri-State `serviceRunning | serviceStopped | serviceUnknown`:
  - `stopped` → Stop wird übersprungen (`service_stop_skipped`, reason `not_running`), am Ende **kein** Start. Löst das eigentliche Problem: das Stop-Kommando gibt auch bei bereits gestopptem Dienst 0 zurück, das sah vorher aus wie „wir haben gestoppt".
  - `running` → wie bisher stoppen; Erfolg setzt `serviceWasStopped = true`.
  - `unknown` → interaktive Rückfrage „Service trotzdem stoppen?" (Default **ja**). Unter `--no-prompt` liefert `prompt.Always{true}` das Ja ⇒ altes Verhalten für Automation.
  - Stop schlägt fehl + Nutzer macht weiter ⇒ `serviceWasStopped = false` ⇒ am Ende **kein** Start.
  - Alle Start-Stellen (Abbruchpfade + Erfolgspfad) laufen jetzt über **eine** Closure `maybeStartService()` (`startSkipped|startOK|startFailed`), die auch die NDJSON-Events feuert. Vorher hatte der Erfolgspfad seinen eigenen Block und prüfte nur `!f.skipService` — das war Lücke Nr. 2.
  - Escape-Hatch: **`--force-start-service`** startet am Ende immer (außer `--skip-service` / `--dry-run`).
  - Neue NDJSON-Events: `service_stop_skipped`, `service_start_skipped`.
  - `probeServiceRunning` existiert weiter als dünner Adapter für den Dry-Run-Check (`unknown` ⇒ ok).
- **Auto-Close bei Doppelklick (Windows):** Nach einem **fehlerfreien** Lauf (`exit 0`) zählt eine `\r`-Zeile 10 s runter und das Fenster schließt sich; **Enter schließt sofort**. Nur wenn das Konsolenfenster uns allein gehört — `ownsConsole()` in `cmd/updater/console_windows.go` fragt `kernel32!GetConsoleProcessList` (via `syscall.NewLazyDLL`, stdlib): Rückgabe `1` = nur unser Prozess hängt dran ⇒ Explorer hat das Fenster für uns erzeugt. Aus `cmd`/PowerShell/Terminal gestartet hängt die Shell mit dran (≥2) ⇒ kein Countdown, `0` (keine Konsole) ebenso. `console_other.go` (`//go:build !windows`) gibt immer `false`.
  - Weitere Bedingungen (in `main.go` nach `runApp`): `!--json`, `!--detach`, `isTerminal(os.Stderr)`. Bei **Fehler-Exit bewusst KEIN Countdown** — Fenster bleibt offen, Meldung lesbar.
  - Dauer: `--close-delay N` (Default **10**, `0` = aus).
  - Ausgabe geht an `os.Stderr`, **nicht** an den MultiWriter (Gotcha #1) → `\r`-Frames bleiben aus dem Logfile.
  - Code: `cmd/updater/countdown.go` — `closeCountdown(w, keys, ticks, secs, format)` ist bewusst mit injizierten `keys`/`ticks`-Channels gebaut, damit Tests ohne echtes Sleep laufen; `waitBeforeClose` verdrahtet `time.Ticker` + eine stdin-Goroutine (wird nie gejoined — der Prozess exitet direkt danach). i18n-Feld `ConsoleClosing`.
- **Conf-Dateien überschreibbar (0.12.0):** `conf.files` in `conf/updater.properties` listet Konfigurationsdateien, die das Release mitbringt und die auf Wunsch mit der Release-Version überschrieben werden. **Standard aus.** Ablauf: `sync.CompareConfFiles` vergleicht ref vs. live (`ConfSame|ConfModified|ConfMissingLive|ConfMissingRef`), abweichende/fehlende werden gelistet, dann Prompt mit Default **Nein**. `--update-conf` = ja ohne Rückfrage; unter `--no-prompt` **ohne** das Flag passiert nichts (`prompt.Always{true}` würde sonst versehentlich Ja liefern → wird explizit umgangen). Geschrieben wird erst **nach** `sync.Apply`, also nach dem Backup. `ConfMissingRef` (Eintrag nicht im Release) bleibt unangetastet — eine veraltete `conf.files`-Zeile darf keine Live-Config löschen. Preflight prüft die Zieldateien mit (`PreflightConfFiles`).
  - **Wichtig:** Der Vergleich läuft **vor** den Early-Returns `--dry-run` und `NoChanges`. Sonst würde ein Release, bei dem sich nur eine Default-Config geändert hat, als „keine Änderungen" durchrutschen. Der Diff-Review-Loop wird übersprungen, wenn nur Conf-Dateien abweichen (es gibt keine Dateiliste zu reviewen).
  - Listen für die zwei Produkte stehen als Kommentar in `conf/updater.properties` (tEXAM: 7 `*-defaults.properties`; tOSCE: `conf/server.default.properties`). **Der Properties-Parser kennt keine `\`-Fortsetzungszeilen** → Liste muss einzeilig bleiben.
- **`backup.include` (0.12.0):** komma-getrennte Pfade (Dateien **oder** Verzeichnisse, relativ zum App-Root), die **nur** ins Datei-Backup wandern — nie verglichen, nie überschrieben. Sample-Default: `conf`. Fehlende Pfade werden still übersprungen. Zusätzlich werden bei bestätigtem Conf-Update die betroffenen `conf.files` explizit in `BackupOptions.Include` gehängt (`backupIncludePaths`) — bewusst **nicht** auf die Überlappung mit `backup.include` vertraut, sonst verschwindet die Rollback-Kopie sobald jemand `backup.include` kürzt.
- **Update-Prompt defaultet auf Ja (0.12.0):** „Update jetzt durchführen?" ist jetzt `[J/n]` statt `[j/N]`. Nur der `def`-Parameter von `prompter.Confirm`; das Suffix erzeugt `Stdin.suffix(def)` selbst, kein i18n-Text betroffen. `--no-prompt` unberührt (`Always{true}` ignoriert den Default ohnehin).
- **Weitere Flags:** `--zip`, `--config`, `--app-root`, `--dry-run` (Pre-Flight-Checks), `--skip-service`, `--ignore-service-errors`, `--force-start-service`, `--update-conf`, `--download-parts`, `--diff-workers`, `--no-files-backup`, `--no-db-backup`, `--no-preflight`, `--close-delay`, `--no-prompt`, `--detach`, `--logfile`, `--json`, `--lang`, `--version`.

---

## 7. Versionshistorie (Kurz)

- **0.12.0** (a) **`conf.files`**: gelistete Konfigurationsdateien lassen sich auf Nachfrage aus dem Release überschreiben (Default aus, `--update-conf` für Automation). Vergleich vor dem Prompt, nur Abweichendes wird geschrieben, Ausführung nach dem Backup, Preflight deckt sie mit ab. (b) **`backup.include`**: zusätzliche Pfade (Dateien/Verzeichnisse), die ausschliesslich gesichert werden — Sample-Default `conf`. `BackupDirs` löst Sync-Dirs + Include vorab in eine deduplizierte Entry-Liste auf. (c) **Update-Prompt** defaultet auf **Ja** (`[J/n]`). (d) Neue NDJSON-Events `conf_files_compared`, `conf_files_skipped`, `conf_files_applied`, `conf_files_failed`.
- **0.11.1** Doppelklick-Fenster auf Windows schliesst sich nach fehlerfreiem Lauf selbst: 10-s-Countdown (`--close-delay N`, `0` = aus), Enter schliesst sofort. Nur wenn das Konsolenfenster uns allein gehoert (`ownsConsole()` via `kernel32!GetConsoleProcessList` == 1); aus einer Shell gestartet passiert nichts. Bei Fehler-Exit bleibt das Fenster offen. Neues i18n-Feld `ConsoleClosing`.
- **0.11.0** (a) **Download parallelisiert**: N Range-Requests (`--download-parts`, Default 4), Part-Retry mit Offset-Resume, 1-MiB-Buffer, kein `f.Sync()` mehr, h2 bei Parallelbetrieb abgewählt; Statuszeile mit MB/s + ETA. (b) **Dienst wird nur noch gestartet, wenn tUPDATE ihn gestoppt hat** — Tri-State-Statusprobe vor dem Stop, Rückfrage bei unklarem Status, `--force-start-service` als Escape-Hatch, neue Events `service_stop_skipped`/`service_start_skipped`. (c) **Diff-Fortschritt sichtbar** (`diffTicker`). (d) **Diff schneller**: Worker-Pool (`--diff-workers`, Default `min(NumCPU,8)`), parallele Baum-Walks, Block-Vergleich mit Early-Exit statt Doppel-Hash → xxhash-Dependency entfällt.
- **0.10.2** Fix: Signaturkette unvollständig — nur das Leaf steckte im Signaturblock, weil `-pkcs11cert` das `-certs`-Chain-File aussticht. Windows zeigte deshalb trotz gültiger Signatur „Unbekannter Herausgeber" im UAC-Dialog. `tools/sign-windows.sh` gibt das Intermediate jetzt per `-ac signing/inter.pem` mit und zählt nach dem Signieren die Zerts im Block (`<2` → Abbruch). Verify gegen `signing/root.pem` allein: ok.
- **0.10.1** Windows-Binaries (`amd64`/`arm64`) Authenticode-signiert (Certum Open Source Code Signing, SimplySign-Cloud-HSM). Neues `make sign` (`tools/sign-windows.sh`): dynamische Cert-ID via `pkcs11-tool`, RFC3161-Timestamp (`time.certum.pl`), läuft nach `upx-all`. `signing/` gitignored. Beseitigt „Unknown publisher" (UAC), baut SmartScreen-Reputation.
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

- **Nutzer testet UAC-Herausgeber auf Windows** mit 0.10.2: erwartet „Verifizierter Herausgeber: Open Source Developer Heid Jörn" statt „Unbekannter Herausgeber". **Rückmeldung steht noch aus.** Die v0.10.1-Release-Assets tragen weiterhin die unvollständige Kette — nicht mehr verteilen.
- **Version-Resource ist stale:** `cmd/updater/versioninfo.json` steht fest auf `0.1.0.0`, CompanyName/LegalCopyright = `tUPDATE`. Datei-Eigenschaften → Details zeigen also weder die echte Version noch den Namen. Nicht sicherheitsrelevant (Herausgeber kommt aus der Signatur), aber kosmetisch offen; Fix hieße `versioninfo.json` aus `VERSION` generieren.
- **Neu offen (0.11.0):** Paralleler Download + schnellerer Diff sind auf diesem Mac gemessen, aber **nicht auf dem echten Zielserver** verifiziert. Beim ersten Praxistest prüfen: (1) beantwortet der Release-Server Range-Requests (sonst still Single-Stream, sichtbar an fehlendem Speedup)? (2) MB/s-Anzeige — bleibt sie bei mehr Teilen gleich, limitiert der Server-Uplink und `--download-parts` bringt nichts. (3) Liegt die Installation auf einem Netzlaufwerk/HDD, ggf. `diff.workers = 1` oder `2` setzen.
- **Nutzer testet Backup-Hang auf Windows** mit Stufe `min` (Release 0.9.1+). Erwartung: Balken läuft zügig → war reine `max`-Rechenzeit. Falls trotz `min` bei 0 % → echter Bug, dann an `internal/archive/backup.go` (`writeTarXzBackup`) ansetzen. **Rückmeldung steht noch aus.**
- **Icon-Anzeige auf echtem Windows** ungeprüft (kein Windows hier). Einbettung + Build verifiziert; UPX behält Icon/Version-Resourcen normalerweise sichtbar.
- **Apply-Retry (Option A)** offen: Preflight (0.10.0) deckt steady-state-Locks ab, aber Virenscanner-Flacker zwischen Probe und `sync.Apply` bleibt ein Race → bei Bedarf Retry mit Backoff in `internal/sync/apply.go::copyFile`/`os.Remove` ergänzen.
- **0.12.0 nicht am echten Server getestet:** `conf.files` und `backup.include` sind durch Unit-/End-to-End-Tests plus einen lokalen Smoke-Lauf abgedeckt (Prompt-Kette DE, Backup enthielt die Vor-Update-Fassung), aber nie gegen ein echtes tEXAM-/tOSCE-Release gelaufen. Beim ersten Praxistest prüfen: (1) heissen die sieben tEXAM-Dateien im Release-ZIP wirklich so und liegen sie unter `conf/`? Falls nicht, meldet tUPDATE sie als `nicht im Release - bleibt` und tut brav nichts — kein Fehler, aber auch kein Update. (2) landet die alte Fassung im Backup-Archiv? (3) `conf.files` muss **einzeilig** bleiben — der Properties-Parser kennt keine `\`-Fortsetzungszeilen.
- **Release 0.12.0 ist raus** (Commit `bcd9757`, Tag `v0.12.0`, 7 Assets). Windows-`.exe` signiert, Ketten-Test gegen `~/.certum/root.pem` allein: `Signature verification: ok`. `windows-arm64` bleibt ungepackt (7,2 MB statt 2,6 MB) — UPX 5.2 kann `win64/arm64` weiterhin nicht.
- Optional angeboten, nicht umgesetzt: macOS `.app`-Bundle mit `.icns`.

---

## 9. Persönliche Memory-Fakten (dupliziert, da `~/.claude/.../memory/` NICHT mitgesichert wird)

- **Kein `Co-Authored-By`** in Commits/PRs (Nutzer-Vorgabe, nur ab jetzt; History NICHT umschreiben außer explizit verlangt).
- **GitHub immer als `MeMyselfI`** für dieses Repo.
- Nutzer-E-Mail (Kontext): `claude@agentsinaction.de` bzw. `jornheid`. Mac, zsh, Homebrew vorhanden.
