package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"updater/internal/archive"
	"updater/internal/config"
	"updater/internal/download"
	"updater/internal/i18n"
	"updater/internal/machine"
	"updater/internal/paths"
	"updater/internal/prompt"
	"updater/internal/service"
	"updater/internal/sync"
)

const (
	exitOK           = 0
	exitConfig       = 1
	exitDownload     = 2
	exitExtract      = 3
	exitServiceStop  = 4
	exitSync         = 5
	exitServiceStart = 6
	exitUserAbort    = 7
	exitDryRunCheck  = 9
)

// flagSet holds parsed command-line options.
type flagSet struct {
	zipPath             string
	configPath          string
	appRoot             string
	logfile             string
	dryRun              bool
	noPrompt            bool
	skipService         bool
	noFilesBackup       bool
	noDBBackup          bool
	ignoreServiceErrors bool
	detach              bool
	jsonOut             bool
	lang                string
	backupFormat        string
	backupCompression   string
	showVersion         bool
}

// runApp executes the updater workflow.
//
// stdout / stderr are for localized human output; emitWriter is for the
// machine-readable NDJSON stream. In --json mode the caller is expected to
// pass io.Discard for stdout/stderr (so localized text vanishes) and the
// real stdout/multiwriter for emitWriter. In human mode it's the opposite.
//
// Returns one of the exit* constants.
func runApp(stdin io.Reader, stdout, stderr, emitWriter io.Writer, args []string, version string) (code int) {
	f, err := parseFlags(args, stdout, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	// --lang is validated before --version so an invalid value is reported
	// even when the user just wanted to print the version.
	lang, ok := resolveLang(f.lang)
	if !ok {
		fmt.Fprintf(stderr, "--lang: invalid value %q, expected de|en|fr\n", f.lang)
		return exitConfig
	}
	s := i18n.Get(lang)

	if f.showVersion {
		fmt.Fprintf(stdout, "tUPDATE %s\n", version)
		return exitOK
	}

	if f.jsonOut && !f.noPrompt {
		fmt.Fprintln(stderr, "--json requires --no-prompt")
		return exitConfig
	}

	// JSON mode contract: localized human writes from inside runApp must not
	// leak. We honor it here regardless of caller discipline so tests + future
	// callers can rely on the same behavior main.go provides.
	if f.jsonOut {
		stdout = io.Discard
		stderr = io.Discard
	}

	emit := machine.New(emitWriter, f.jsonOut)
	defer func() { emit.Exit(code) }()

	appRoot, err := resolveAppRoot(f.appRoot)
	if err != nil {
		fmt.Fprintln(stderr, s.ConfigError, err)
		emit.FatalError("app_root", err.Error())
		return exitConfig
	}

	configPath := f.configPath
	if configPath == "" {
		configPath = filepath.Join(appRoot, "conf", "updater.properties")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, s.ConfigError, err)
		emit.FatalError("config", err.Error())
		return exitConfig
	}

	stopCmd := cfg.StopCommand(runtime.GOOS)
	startCmd := cfg.StartCommand(runtime.GOOS)
	if !f.skipService && (stopCmd == "" || startCmd == "") {
		fmt.Fprintf(stderr, s.NoServiceCommandConfig+"\n", runtime.GOOS)
		emit.FatalError("config", "no service commands for "+runtime.GOOS)
		return exitConfig
	}

	prompter := newPrompter(stdin, stdout, f.noPrompt, s)

	// --dry-run pre-flight: prove the destructive steps would succeed
	// (writable backup dir, pg_dump binary present, download URL reachable)
	// without actually mutating anything. If --zip is given we still fall
	// through to extract+diff so operators see "what would change".
	if f.dryRun {
		allOK := runDryRunChecks(cfg, appRoot, f, emit, stderr, s)
		if !allOK {
			return exitDryRunCheck
		}
		if f.zipPath == "" {
			// No local ZIP, only the URL probe was possible. We deliberately
			// do not run the full download in --dry-run.
			fmt.Fprintln(stderr, s.DryRunDone)
			return exitOK
		}
		// Continue: extract + diff against the user-provided local ZIP.
	}

	zipFile, cleanupZip, exitCode := acquireZip(f, cfg, emit, stderr, s)
	if exitCode != exitOK {
		return exitCode
	}
	defer cleanupZip()

	tempDir, err := os.MkdirTemp("", "updater-extract-*")
	if err != nil {
		fmt.Fprintln(stderr, s.TempDirError, err)
		emit.FatalError("extract", err.Error())
		return exitExtract
	}
	defer os.RemoveAll(tempDir)

	fmt.Fprintln(stderr, s.Extracting)
	if err := archive.Extract(zipFile, tempDir, ttyProgress(f)); err != nil {
		fmt.Fprintln(stderr, s.ExtractError, err)
		emit.FatalError("extract", err.Error())
		return exitExtract
	}

	runner := service.New(runtime.GOOS)
	runner.Stdout = stderr
	runner.Stderr = stderr

	serviceWasStopped := false
	maybeStartService := func() {
		if !serviceWasStopped {
			return
		}
		fmt.Fprintln(stderr, s.ServiceStarting, startCmd)
		startCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		defer cancel()
		if err := runner.Run(startCtx, startCmd, time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second); err != nil {
			fmt.Fprintln(stderr, s.ServiceStartError, err)
		}
	}

	if !f.skipService && !f.dryRun {
		fmt.Fprintln(stderr, s.ServiceStopping, stopCmd)
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		err := runner.Run(stopCtx, stopCmd, time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, s.ServiceStopError, err)
			emit.ServiceStopFailed(err.Error())
			// --ignore-service-errors forces continue.
			// Otherwise --no-prompt aborts and interactive mode asks.
			cont, perr := promptContinue(prompter, f.noPrompt, f.ignoreServiceErrors, s, stderr)
			if perr != nil || !cont {
				return exitServiceStop
			}
			serviceWasStopped = false
		} else {
			emit.ServiceStopOK()
			serviceWasStopped = true
		}
	}

	refRoot := sync.ResolveRefRoot(tempDir, cfg.SyncDirectories)
	if refRoot != tempDir {
		fmt.Fprintf(stderr, s.WrapperDetected+"\n", filepath.Base(refRoot))
	}
	emit.ExtractDone(refRoot)

	fmt.Fprintln(stderr, s.ComputingDiff)
	diffs, err := sync.Compute(refRoot, appRoot, cfg.SyncDirectories)
	if err != nil {
		fmt.Fprintln(stderr, s.DiffError, err)
		emit.FatalError("diff", err.Error())
		maybeStartService()
		return exitSync
	}

	fmt.Fprint(stdout, sync.FormatReport(diffs, s.ReportTotal, s.ReportNoDirs))
	summary := sync.Summarize(diffs)
	emit.Diff(summary.Added, summary.Modified, summary.Removed, diffPerDirMap(diffs))

	if f.dryRun {
		fmt.Fprintln(stderr, s.DryRunDone)
		maybeStartService()
		return exitOK
	}
	if !summary.HasChanges() {
		fmt.Fprintln(stderr, s.NoChanges)
		maybeStartService()
		return exitOK
	}

	// Three-way prompt: continue / abort / show full file list.
	if !runDiffReviewLoop(prompter, diffs, s, stdout, stderr) {
		fmt.Fprintln(stderr, s.UpdateAborted)
		maybeStartService()
		return exitUserAbort
	}

	var wantBackup bool
	if f.noFilesBackup {
		wantBackup = false
	} else {
		wantBackup, err = prompter.Confirm(s.BackupQuestion, true)
		if err != nil {
			fmt.Fprintln(stderr, s.PromptError, err)
			maybeStartService()
			return exitUserAbort
		}
	}

	backupTs := time.Now()
	backupDir := filepath.Join(appRoot, cfg.BackupDirectory)

	var backupPath string
	if wantBackup {
		backupOpts, err := resolveBackupOptions(f, prompter, s)
		if err != nil {
			fmt.Fprintln(stderr, s.BackupError, err)
			maybeStartService()
			return exitUserAbort
		}
		fmt.Fprintf(stderr, "%s (%s / %s)\n", s.BackupCreating, backupOpts.Format, backupOpts.Level)
		p, err := archive.BackupDirs(appRoot, backupDir, cfg.SyncDirectories, backupTs, backupOpts, ttyProgress(f))
		if err != nil {
			fmt.Fprintln(stderr, s.BackupError, err)
			emit.BackupFilesFailed(err.Error())
			maybeStartService()
			return exitSync
		}
		backupPath = p
		fmt.Fprintln(stderr, s.BackupLabel, backupPath)
		emit.BackupFilesOK(backupPath, fileSize(backupPath))
	}

	var wantDBBackup bool
	if f.noDBBackup {
		wantDBBackup = false
	} else {
		wantDBBackup, err = prompter.Confirm(s.DBBackupQuestion, false)
		if err != nil {
			fmt.Fprintln(stderr, s.PromptError, err)
			maybeStartService()
			return exitUserAbort
		}
	}
	if wantDBBackup {
		pgBin := resolvePgDumpBinary(cfg.PgdumpBinary(runtime.GOOS))
		if pgBin == "" {
			fmt.Fprintln(stderr, s.DBBackupSkipped)
			emit.BackupDBSkipped("pg_dump_not_found")
		} else {
			dumpPath := archive.PgDumpPath(backupDir, backupTs)
			fmt.Fprintln(stderr, s.DBBackupStarting)
			dumpCtx, dumpCancel := context.WithTimeout(context.Background(), archive.PgDumpTimeout)
			err := archive.RunPgDump(dumpCtx, archive.PgDumpOptions{
				Binary:      pgBin,
				OutFile:     dumpPath,
				ExtraArgs:   cfg.PgdumpArgs,
				ExtraEnv:    buildPgDumpEnv(cfg),
				HasPassword: cfg.PgdumpPassword != "",
			}, stderr)
			dumpCancel()
			if err != nil {
				fmt.Fprintln(stderr, s.DBBackupFailed, err)
				emit.BackupDBFailed(err.Error())
			} else {
				fmt.Fprintln(stderr, s.DBBackupDone, dumpPath)
				emit.BackupDBOK(dumpPath, fileSize(dumpPath))
			}
		}
	}

	wantUpdate, err := prompter.Confirm(s.UpdateQuestion, false)
	if err != nil {
		fmt.Fprintln(stderr, s.PromptError, err)
		maybeStartService()
		return exitUserAbort
	}
	if !wantUpdate {
		fmt.Fprintln(stderr, s.UpdateAborted)
		maybeStartService()
		return exitUserAbort
	}

	fmt.Fprintln(stderr, s.ApplyingUpdate)
	tick := applyTicker(f)
	err = sync.Apply(refRoot, appRoot, diffs, tick)
	if tick != nil {
		fmt.Fprintln(os.Stderr) // finish the single-line ticker
	}
	if err != nil {
		fmt.Fprintln(stderr, s.SyncError, err)
		emit.ApplyFailed(err.Error())
		if backupPath != "" {
			fmt.Fprintln(stderr, s.RestoreFromBackup, backupPath)
		}
		maybeStartService()
		return exitSync
	}
	fmt.Fprintln(stderr, s.UpdateSuccess)
	emit.ApplyOK()

	if !f.skipService {
		fmt.Fprintln(stderr, s.ServiceStarting, startCmd)
		startCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		err := runner.Run(startCtx, startCmd, time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, s.ServiceStartError, err)
			emit.ServiceStartFailed(err.Error())
			cont, perr := promptContinue(prompter, f.noPrompt, f.ignoreServiceErrors, s, stderr)
			if perr != nil || !cont {
				return exitServiceStart
			}
		} else {
			emit.ServiceStartOK()
		}
	}

	fmt.Fprintln(stderr, s.Done)
	return exitOK
}

// fileSize is a best-effort os.Stat wrapper used to enrich emit events. A
// stat failure is not worth aborting the workflow for, so we just report 0.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// ttyProgress returns a throttled archive.Progress that animates a single
// carriage-return percentage line below the caller's own status line (e.g.
// "Entpacken..."). It carries no text label of its own to avoid repeating that
// status text. The animation is written directly to os.Stderr — the real
// console — rather than the run-time stderr writer, which is an io.MultiWriter
// teeing into the logfile (so \r would corrupt it). It is enabled only when
// os.Stderr is an interactive terminal and disabled under --json or --detach.
// Returns nil to disable, so callers can pass the result straight through.
func ttyProgress(f *flagSet) archive.Progress {
	if f.jsonOut || f.detach || !isTerminal(os.Stderr) {
		return nil
	}
	var last time.Time
	return func(done, total int64) {
		now := time.Now()
		if done < total && now.Sub(last) < 100*time.Millisecond {
			return
		}
		last = now
		pct := 100
		if total > 0 {
			pct = int(done * 100 / total)
		}
		fmt.Fprintf(os.Stderr, "\r  %3d%% (%s / %s)   ", pct, fmtBytes(done), fmtBytes(total))
		if done >= total {
			fmt.Fprintln(os.Stderr)
		}
	}
}

// applyTicker returns a per-file callback for sync.Apply that streams the
// current path on a single carriage-return line on the real console. Disabled
// (nil) under --json, --detach, or a non-terminal stderr. Lightly throttled so
// huge diffs don't flood the terminal.
func applyTicker(f *flagSet) func(string) {
	if f.jsonOut || f.detach || !isTerminal(os.Stderr) {
		return nil
	}
	var last time.Time
	return func(name string) {
		now := time.Now()
		if now.Sub(last) < 16*time.Millisecond {
			return
		}
		last = now
		const w = 64
		if len(name) > w {
			name = "..." + name[len(name)-(w-3):]
		}
		fmt.Fprintf(os.Stderr, "\r  %-*s", w, name)
	}
}

// isTerminal reports whether f is a character device (a TTY), so progress
// animation is only emitted to a real terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// fmtBytes renders a byte count in binary units for the progress line.
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// diffPerDirMap converts the diff slice into a nested map shape suitable for
// the {"added":N,"modified":M,"removed":K} per-directory JSON payload.
func diffPerDirMap(diffs []sync.DirDiff) map[string]map[string]int {
	out := make(map[string]map[string]int, len(diffs))
	for i := range diffs {
		a, m, r := diffs[i].Counts()
		out[diffs[i].Dir] = map[string]int{"added": a, "modified": m, "removed": r}
	}
	return out
}

// runDryRunChecks fires the pre-flight checks for --dry-run.
// Returns true if every check passed.
func runDryRunChecks(cfg *config.Config, appRoot string, f *flagSet, emit *machine.Emitter, stderr io.Writer, s i18n.Strings) bool {
	allOK := true

	// runCheck wraps the emit + stderr + allOK plumbing so each individual
	// probe can stay focused on the "what to test" question.
	runCheck := func(name string, fatal bool, ok bool, detail string) {
		emit.DryRunCheck(name, ok, detail)
		fmt.Fprintf(stderr, "Check %s: %s (%s)\n", name, okOrFail(ok), detail)
		if !ok && fatal {
			allOK = false
		}
	}

	// 1. Backup directory writable.
	backupDir := filepath.Join(appRoot, cfg.BackupDirectory)
	bdOK, bdDetail := probeBackupDirWritable(backupDir)
	runCheck("backup_dir_writable", true, bdOK, bdDetail)

	// 2. Service binaries (skip when --skip-service).
	if !f.skipService {
		stopOK, stopDetail := probeServiceBinary(cfg.StopCommand(runtime.GOOS))
		runCheck("service_stop_binary", true, stopOK, stopDetail)

		startOK, startDetail := probeServiceBinary(cfg.StartCommand(runtime.GOOS))
		runCheck("service_start_binary", true, startOK, startDetail)

		// Whether the service is actually running — informational, never fatal.
		// An inconclusive result (unknown service manager, status tool absent,
		// unparseable output, timeout) reports ok=true so a "couldn't tell"
		// never blocks dry-run; only a clear "stopped" reports ok=false.
		runOK, runDetail := probeServiceRunning(runtime.GOOS, cfg.StopCommand(runtime.GOOS))
		runCheck("service_running", false, runOK, runDetail)
	}

	// 3. pg_dump binary + connection params (skip when --no-db-backup).
	if !f.noDBBackup {
		pgBin := resolvePgDumpBinary(cfg.PgdumpBinary(runtime.GOOS))
		pgOK := pgBin != ""
		pgDetail := pgBin
		if !pgOK {
			pgDetail = "pg_dump_not_found"
		}
		runCheck("pgdump_binary", true, pgOK, pgDetail)

		// host / port: informational (libpq has sane defaults).
		hostOK, hostDetail := probePgdumpString(cfg.PgdumpHost, "PGHOST", "pgdump.host")
		runCheck("pgdump_conn_host", false, hostOK, hostDetail)

		// database / user: required.
		dbOK, dbDetail := probePgdumpString(cfg.PgdumpDB, "PGDATABASE", "pgdump.db")
		runCheck("pgdump_conn_database", true, dbOK, dbDetail)

		userOK, userDetail := probePgdumpString(cfg.PgdumpUser, "PGUSER", "pgdump.user")
		runCheck("pgdump_conn_user", true, userOK, userDetail)

		// password: required, but check three sources without ever emitting the value.
		pwOK, pwDetail := probePgdumpPassword(cfg.PgdumpPassword)
		runCheck("pgdump_conn_password", true, pwOK, pwDetail)

		// connectivity: informational — pg_isready optional, never fatal.
		connOK, connDetail := probePgIsReady(cfg)
		runCheck("pgdump_connectivity", false, connOK, connDetail)
	}

	// 4. Download URL probe — only when no local --zip is supplied.
	if f.zipPath == "" {
		urlOK, urlDetail := probeDownloadURL(cfg)
		runCheck("download_url", true, urlOK, urlDetail)
	}

	return allOK
}

// probeServiceBinary takes the configured service.stop / service.start command
// (e.g. "launchctl stop org.example.myapp"), takes the first token and asks
// exec.LookPath whether that command is callable. We deliberately don't try
// to query the service itself — that would require parsing platform-specific
// output, and the failure mode we care about ("launchctl not installed") is
// already covered by the binary check.
func probeServiceBinary(cmd string) (bool, string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false, "empty command"
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false, "empty command"
	}
	bin := fields[0]
	path, err := exec.LookPath(bin)
	if err != nil {
		return false, bin + ": not found on PATH"
	}
	return true, path
}

// probeServiceRunning is the complement to probeServiceBinary: the latter only
// proves the control binary (launchctl / systemctl / sc) is callable; this one
// tries to learn whether the managed service is actually running, so a dry-run
// can warn that the live update has nothing to stop.
//
// It is deliberately best-effort. The service identifier is parsed from the
// configured service.stop command (last token, e.g. "tosce" / "tOSCE-Server"),
// and a read-only status query is run for the recognised managers. Anything we
// cannot determine — an unknown manager, a missing status tool, unparseable
// output, or a timeout — returns ok=true so an inconclusive probe never blocks
// dry-run. Only a clearly stopped/absent service returns ok=false.
func probeServiceRunning(goos, stopCmd string) (bool, string) {
	manager, id := parseServiceTarget(stopCmd)
	if manager == "" || id == "" {
		return true, "inconclusive: cannot derive service from stop command"
	}

	statusBin := manager
	if manager == "net" {
		// net has no machine-readable per-service query; sc query does.
		statusBin = "sc"
	}
	binPath, err := exec.LookPath(statusBin)
	if err != nil {
		return true, "inconclusive: " + statusBin + " not on PATH (skipped)"
	}

	switch manager {
	case "launchctl":
		out, runErr, timedOut := runStatusCmd(binPath, "list", id)
		if timedOut {
			return true, "inconclusive: launchctl status query timed out"
		}
		return interpretLaunchctl(out, runErr)
	case "systemctl":
		out, _, timedOut := runStatusCmd(binPath, "is-active", id)
		if timedOut {
			return true, "inconclusive: systemctl status query timed out"
		}
		return interpretSystemctl(out)
	case "net", "sc":
		return probeWindowsServiceRunning(binPath, id)
	default:
		return true, "inconclusive: unsupported service manager " + manager
	}
}

// runStatusCmd runs a read-only status command with a short timeout. timedOut
// is reported separately so callers can mark the probe inconclusive instead of
// misreading a slow/hung status tool as a definitive "not running".
func runStatusCmd(bin string, args ...string) (out string, err error, timedOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", e, true
	}
	return strings.TrimSpace(string(b)), e, false
}

// probeWindowsServiceRunning resolves a Windows service's run state via sc.
// `net start`/`net stop` accept either the service key name or the display
// name, but `sc query` only understands the key name. So when the first query
// reports the service does not exist (error 1060) we treat id as a display
// name, resolve it to the key name with `sc getkeyname`, and query again.
func probeWindowsServiceRunning(scPath, id string) (bool, string) {
	out, runErr, timedOut := runStatusCmd(scPath, "query", id)
	if timedOut {
		return true, "inconclusive: sc status query timed out"
	}
	if classifySc(out, runErr) != scNotFound {
		return interpretSc(out, runErr)
	}

	// id may be a display name; resolve it to the service key name and retry.
	key := scResolveKeyName(scPath, id)
	if key == "" || strings.EqualFold(key, id) {
		return interpretSc(out, runErr) // genuinely not installed under that name
	}
	out2, runErr2, timedOut2 := runStatusCmd(scPath, "query", key)
	if timedOut2 {
		return true, "inconclusive: sc status query timed out"
	}
	if classifySc(out2, runErr2) == scNotFound {
		return interpretSc(out, runErr)
	}
	ok, detail := interpretSc(out2, runErr2)
	return ok, detail + " (display name " + id + " -> service " + key + ")"
}

// scResolveKeyName runs `sc getkeyname <displayName>` and returns the resolved
// service key name, or "" if the lookup failed.
func scResolveKeyName(scPath, displayName string) string {
	out, err, timedOut := runStatusCmd(scPath, "getkeyname", displayName)
	if err != nil || timedOut {
		return ""
	}
	return parseScKeyName(out)
}

// parseServiceTarget extracts the service manager and the service identifier
// from a configured service.stop command. The manager is the basename of the
// first token; the identifier is the last token (the conventional position for
// the unit/label/service name across launchctl, systemctl and net/sc). A
// leading-dash last token (a flag) or an unrecognised manager yields empties so
// the caller reports the probe inconclusive.
func parseServiceTarget(stopCmd string) (manager, id string) {
	fields := strings.Fields(strings.TrimSpace(stopCmd))
	if len(fields) < 2 {
		return "", ""
	}
	bin := strings.ToLower(strings.TrimSuffix(commandBasename(fields[0]), ".exe"))
	switch bin {
	case "launchctl", "systemctl", "net", "sc":
		manager = bin
	default:
		return "", ""
	}
	id = strings.Trim(fields[len(fields)-1], `"'`)
	if id == "" || strings.HasPrefix(id, "-") {
		return "", ""
	}
	return manager, id
}

// commandBasename strips any directory prefix from the first token of a service
// command. Unlike filepath.Base it recognises both '/' and '\' regardless of
// the host OS, because the configured command targets the deployment OS (which
// may differ from the build/test host).
func commandBasename(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// interpretLaunchctl maps `launchctl list <label>` output to a running verdict.
// A non-zero exit means the label is not loaded → not running. When loaded, the
// printed dict carries a "PID" key only while the job has a live process.
func interpretLaunchctl(out string, runErr error) (bool, string) {
	if runErr != nil {
		return false, "not running (launchctl: label not loaded)"
	}
	if strings.Contains(out, "\"PID\"") || strings.Contains(out, "PID =") {
		return true, "running (launchctl reports a PID)"
	}
	if strings.Contains(out, "{") {
		return false, "not running (launchctl: loaded, no PID)"
	}
	return true, "inconclusive: unparseable launchctl output"
}

// interpretSystemctl maps `systemctl is-active <unit>` stdout to a verdict.
// The exit code is intentionally ignored: is-active exits non-zero whenever the
// unit isn't active, so the textual state is the reliable signal.
func interpretSystemctl(out string) (bool, string) {
	state := strings.TrimSpace(out)
	// is-active can print multiple lines for templated/multiple units; the
	// first word is the state of interest.
	if nl := strings.IndexAny(state, " \r\n"); nl >= 0 {
		state = state[:nl]
	}
	switch state {
	case "active", "activating", "reloading":
		return true, "running (systemctl: " + state + ")"
	case "inactive", "failed", "deactivating":
		return false, "not running (systemctl: " + state + ")"
	default:
		return true, "inconclusive: systemctl state " + quoteOrEmpty(state)
	}
}

// scState is the run-state classification derived from `sc query` output.
type scState int

const (
	scUnknown scState = iota
	scRunning
	scStopped
	scNotFound
)

// scStateCodeRe matches the locale-independent numeric service state on the
// STATE/ZUSTAND line of `sc query` output, e.g. ": 4  RUNNING". The trailing
// state word is localized on non-English Windows, but the 1..7 code is not. The
// \b after the digit keeps multi-digit fields (TYPE ": 10", PID ": 1234") from
// matching.
var scStateCodeRe = regexp.MustCompile(`:\s*([1-7])\b`)

// classifySc reduces `sc query` output (and its exit error) to an scState. The
// numeric state code is preferred so the check also works on localized Windows;
// English keywords are a fallback, and error 1060 / "does not exist" marks a
// service that is not installed under the queried name.
func classifySc(out string, runErr error) scState {
	if m := scStateCodeRe.FindStringSubmatch(out); m != nil {
		switch m[1] {
		case "4", "2", "5": // RUNNING, START_PENDING, CONTINUE_PENDING
			return scRunning
		case "1", "3": // STOPPED, STOP_PENDING
			return scStopped
		}
		// 6 PAUSE_PENDING / 7 PAUSED: ambiguous → fall through to keyword/unknown.
	}
	upper := strings.ToUpper(out)
	switch {
	case strings.Contains(upper, "RUNNING"), strings.Contains(upper, "START_PENDING"):
		return scRunning
	case strings.Contains(upper, "STOPPED"), strings.Contains(upper, "STOP_PENDING"):
		return scStopped
	case runErr != nil && (strings.Contains(upper, "1060") || strings.Contains(upper, "DOES NOT EXIST")):
		return scNotFound
	}
	return scUnknown
}

// interpretSc maps an sc classification to a (ok, detail) verdict. A missing
// service is a clear "not running"; an unrecognised output is inconclusive
// (ok=true) so a parsing gap never blocks dry-run.
func interpretSc(out string, runErr error) (bool, string) {
	switch classifySc(out, runErr) {
	case scRunning:
		return true, "running (sc: state RUNNING)"
	case scStopped:
		return false, "not running (sc: state STOPPED)"
	case scNotFound:
		return false, "not running (sc: service not found)"
	default:
		return true, "inconclusive: unparseable sc output"
	}
}

// parseScKeyName extracts the service key name from `sc getkeyname` output. The
// success form prints "Name = <key>"; we take the value after '=' on the first
// non-header line so the parse survives a localized "Name" label.
func parseScKeyName(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[SC]") {
			continue
		}
		if i := strings.IndexByte(line, '='); i >= 0 {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

// quoteOrEmpty renders an sc/systemctl state string for a detail message,
// distinguishing an empty value from a present-but-unknown one.
func quoteOrEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return "\"" + s + "\""
}

// probePgdumpString reports whether a libpq value is set by the conf-key, by
// the matching PG* env var, or nowhere. The detail names the winning source
// so the operator can tell which precedence layer is providing the value.
func probePgdumpString(confValue, envName, confKeyName string) (bool, string) {
	if strings.TrimSpace(confValue) != "" {
		return true, "set via " + confKeyName
	}
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		return true, "set via " + envName + " env"
	}
	return false, "not set (conf " + confKeyName + " + env " + envName + " both empty)"
}

// probePgdumpPassword checks the three places libpq looks for a password
// without ever leaking the value itself. The check is satisfied if any one
// of pgdump.password, PGPASSWORD, or a readable ~/.pgpass is present.
func probePgdumpPassword(confValue string) (bool, string) {
	if strings.TrimSpace(confValue) != "" {
		return true, "set via pgdump.password"
	}
	if strings.TrimSpace(os.Getenv("PGPASSWORD")) != "" {
		return true, "set via PGPASSWORD env"
	}
	if home, err := os.UserHomeDir(); err == nil {
		pgpass := filepath.Join(home, ".pgpass")
		if _, err := os.Stat(pgpass); err == nil {
			return true, "set via " + pgpass
		}
	}
	return false, "not set (no pgdump.password, no PGPASSWORD, no ~/.pgpass)"
}

// probePgIsReady runs the pg_isready utility against the same connection
// parameters tUPDATE would hand to pg_dump. Pure read-only side-effect-free
// TCP handshake; the standard PostgreSQL health-check probe.
//
// pg_isready not on PATH is reported as "skipped" with ok=true — it's an
// optional check, not having it should not fail dry-run.
func probePgIsReady(cfg *config.Config) (bool, string) {
	bin, err := exec.LookPath("pg_isready")
	if err != nil {
		return true, "pg_isready not on PATH (skipped)"
	}

	args := []string{"-t", "5"}
	if h := strings.TrimSpace(cfg.PgdumpHost); h != "" {
		args = append(args, "-h", h)
	}
	if p := strings.TrimSpace(cfg.PgdumpPort); p != "" {
		args = append(args, "-p", p)
	}
	if u := strings.TrimSpace(cfg.PgdumpUser); u != "" {
		args = append(args, "-U", u)
	}
	if d := strings.TrimSpace(cfg.PgdumpDB); d != "" {
		args = append(args, "-d", d)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, runErr := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if runErr == nil {
		return true, trimmed
	}
	if trimmed == "" {
		trimmed = runErr.Error()
	}
	return false, trimmed
}

// probeBackupDirWritable creates the backup directory if missing, then writes
// and deletes a probe file. Returns ok and a short detail string for emit.
func probeBackupDirWritable(dir string) (bool, string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, "mkdir " + dir + ": " + err.Error()
	}
	probe := filepath.Join(dir, ".tupdate-write-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		return false, "write " + probe + ": " + err.Error()
	}
	_ = os.Remove(probe)
	return true, dir
}

// probeDownloadURL kicks off the configured download with a Range header
// asking for the first 1 KiB, then cancels the connection. The goal is to
// learn whether the URL is reachable + the HTTP status is 2xx without doing
// the full multi-hundred-megabyte download.
func probeDownloadURL(cfg *config.Config) (bool, string) {
	client, err := download.NewClient(
		time.Duration(cfg.DownloadTimeoutSecs)*time.Second,
		download.ProxyConfig{
			URL: cfg.ProxyURL, User: cfg.ProxyUser,
			Password: cfg.ProxyPassword, NoProxy: cfg.ProxyNoProxy,
		},
	)
	if err != nil {
		return false, "client: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.DownloadURL, nil)
	if err != nil {
		return false, "request: " + err.Error()
	}
	req.Header.Set("Range", "bytes=0-1023")

	resp, err := client.Do(req)
	if err != nil {
		return false, "do: " + err.Error()
	}
	defer resp.Body.Close()

	// Treat any 2xx (200 full body, 206 partial) as reachable. We do not read
	// the body; closing it short-circuits the rest of the transfer.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, fmt.Sprintf("HTTP %d %s", resp.StatusCode, cfg.DownloadURL)
	}
	return false, fmt.Sprintf("HTTP %d %s", resp.StatusCode, cfg.DownloadURL)
}

func okOrFail(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

// newPrompter wires an interactive Stdin prompter localised to s, or
// returns prompt.Always{true} when running with --no-prompt.
// resolveBackupOptions decides the backup format and compression level. An
// explicit --backup-format / --backup-compression flag wins and is validated;
// otherwise the prompter is asked — which, under --no-prompt, returns the
// defaults (tar.xz / default) without prompting. The option slices are ordered
// to match the archive enum values, so the chosen index casts directly.
func resolveBackupOptions(f *flagSet, p prompt.Prompter, s i18n.Strings) (archive.BackupOptions, error) {
	var opts archive.BackupOptions

	if f.backupFormat != "" {
		format, ok := archive.ParseBackupFormat(f.backupFormat)
		if !ok {
			return opts, fmt.Errorf("--backup-format: invalid value %q, expected zip|tar.xz", f.backupFormat)
		}
		opts.Format = format
	} else {
		// Order must match the archive enum: 0=tar.xz, 1=zip.
		idx, err := p.Choose(s.BackupFormatQuestion, []prompt.Choice{
			{Key: "x", Label: "tar.xz"},
			{Key: "z", Label: "zip"},
		}, int(archive.FormatTarXz))
		if err != nil {
			return opts, err
		}
		opts.Format = archive.BackupFormat(idx)
	}

	if f.backupCompression != "" {
		level, ok := archive.ParseCompressionLevel(f.backupCompression)
		if !ok {
			return opts, fmt.Errorf("--backup-compression: invalid value %q, expected min|default|max", f.backupCompression)
		}
		opts.Level = level
	} else {
		// Order must match the archive enum: 0=min, 1=default, 2=max.
		idx, err := p.Choose(s.BackupLevelQuestion, []prompt.Choice{
			{Key: "m", Label: "min"},
			{Key: "s", Label: "default"},
			{Key: "x", Label: "max"},
		}, int(archive.LevelDefault))
		if err != nil {
			return opts, err
		}
		opts.Level = archive.CompressionLevel(idx)
	}

	return opts, nil
}

func newPrompter(stdin io.Reader, stdout io.Writer, noPrompt bool, s i18n.Strings) prompt.Prompter {
	if noPrompt {
		return prompt.Always{Answer: true}
	}
	return &prompt.Stdin{
		In:                   stdin,
		Out:                  stdout,
		MaxAttempts:          3,
		SuffixYesDefault:     s.SuffixYesDefault,
		SuffixNoDefault:      s.SuffixNoDefault,
		SuffixContinueOrShow: s.SuffixContinueOrShow,
		RetryMessage:         s.RetryMessage,
	}
}

// runDiffReviewLoop drives the three-way "continue / abort / show full list"
// prompt that runs immediately after the diff summary. Returns true if the
// workflow should continue, false if the user aborted.
func runDiffReviewLoop(p prompt.Prompter, diffs []sync.DirDiff, s i18n.Strings, stdout, stderr io.Writer) bool {
	for {
		answer, err := p.ConfirmContinueOrShow(s.ContinueOrShowQuestion)
		if err != nil {
			fmt.Fprintln(stderr, s.PromptError, err)
			return false
		}
		switch answer {
		case prompt.AnswerYes:
			return true
		case prompt.AnswerNo:
			return false
		case prompt.AnswerShowAll:
			fmt.Fprintln(stdout, s.DetailsHeader)
			fmt.Fprintln(stdout, "  ", s.LegendAdded, "  ", s.LegendModified, "  ", s.LegendRemoved)
			fmt.Fprint(stdout, sync.FormatDetails(diffs))
			ok, err := p.Confirm(s.ContinueAfterDetailsQuestion, true)
			if err != nil {
				fmt.Fprintln(stderr, s.PromptError, err)
				return false
			}
			return ok
		}
	}
}

// promptContinue asks "Continue anyway?" when a service command fails.
//
//   - ignoreErrors: --ignore-service-errors forces continue without asking.
//     The error message is still printed to stderr and the logfile before
//     we get here, so the operator/CI run keeps the failure trail.
//   - noPrompt: --no-prompt without --ignore-service-errors aborts so
//     automation surfaces service problems instead of swallowing them.
//   - otherwise: interactive y/N confirmation.
func promptContinue(p prompt.Prompter, noPrompt, ignoreErrors bool, s i18n.Strings, stderr io.Writer) (bool, error) {
	if ignoreErrors {
		return true, nil
	}
	if noPrompt {
		return false, nil
	}
	cont, err := p.Confirm(s.ContinueAnyway, false)
	if err != nil {
		fmt.Fprintln(stderr, s.PromptError, err)
		return false, err
	}
	return cont, nil
}

func parseFlags(args []string, stdout, stderr io.Writer) (*flagSet, error) {
	fs := flag.NewFlagSet("updater", flag.ContinueOnError)
	fs.SetOutput(stderr)

	f := &flagSet{}
	fs.StringVar(&f.zipPath, "zip", "", "lokale ZIP statt Download nutzen / use a local ZIP instead of downloading")
	fs.StringVar(&f.configPath, "config", "", "Pfad zu updater.properties (Default: <approot>/conf/updater.properties)")
	fs.StringVar(&f.appRoot, "app-root", "", "App-Root überschreiben (Default: dirname(dirname(executable)))")
	fs.StringVar(&f.logfile, "logfile", "", "Logfile-Pfad (Default: <TempDir>/updater-<ts>.log)")
	fs.BoolVar(&f.detach, "detach", false, "in Hintergrund forken (impliziert --no-prompt) / detach into background")
	fs.BoolVar(&f.dryRun, "dry-run", false, "nur Diff anzeigen, nichts ändern / show diff only")
	fs.BoolVar(&f.noPrompt, "no-prompt", false, "keine Rückfragen (Backup=ja, Update=ja, Service-Fehler=Abbruch)")
	fs.BoolVar(&f.skipService, "skip-service", false, "Service nicht stoppen/starten / skip service stop/start")
	fs.BoolVar(&f.noFilesBackup, "no-files-backup", false, "ZIP-Backup ueberspringen / skip files (ZIP) backup")
	fs.BoolVar(&f.noDBBackup, "no-db-backup", false, "DB-Backup (pg_dump) ueberspringen / skip DB backup")
	fs.BoolVar(&f.ignoreServiceErrors, "ignore-service-errors", false, "bei Stop/Start-Fehler weitermachen / continue past service stop/start failures")
	fs.BoolVar(&f.jsonOut, "json", false, "NDJSON-Events auf stdout (setzt --no-prompt voraus) / NDJSON events on stdout")
	fs.StringVar(&f.lang, "lang", "", "UI-Sprache erzwingen: de | en | fr (Default: aus Env)")
	fs.StringVar(&f.backupFormat, "backup-format", "", "Backup-Format: zip | tar.xz (Default: interaktiv fragen, sonst tar.xz)")
	fs.StringVar(&f.backupCompression, "backup-compression", "", "Kompression: min | default | max (Default: interaktiv fragen, sonst default)")
	fs.BoolVar(&f.showVersion, "version", false, "Version anzeigen / show version")

	fs.Usage = func() { writeHelp(stdout) }

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return f, nil
}

// writeHelp prints the grouped CLI reference + three example invocations to w.
// Sections mix DE/EN to match the flag descriptions; section headers are EN-caps
// so the structure is readable at a glance regardless of locale.
func writeHelp(w io.Writer) {
	const help = `tUPDATE - generic CLI updater for server / file-based installations.

USAGE
  updater [flags]

INPUT / RUN MODE
  --zip <path>             lokale ZIP statt Download / use a local ZIP
  --config <path>          alternative properties-Datei (Default: <approot>/conf/updater.properties)
  --app-root <path>        App-Root explizit setzen (Default: dirname(dirname(executable)))
  --dry-run                nur Diff anzeigen, kein Apply / show diff only

SERVICE
  --skip-service           Service-Stop/-Start auslassen / skip service stop+start
  --ignore-service-errors  bei Stop/Start-Fehler weitermachen statt abbrechen

BACKUP
  --no-files-backup        Datei-Backup-Schritt komplett ueberspringen (Prompt entfaellt)
  --no-db-backup           DB-Backup (pg_dump) komplett ueberspringen (Prompt entfaellt)
  --backup-format <fmt>    zip | tar.xz -- ohne Flag interaktiv gefragt, sonst tar.xz
  --backup-compression <l> min | default | max -- ohne Flag interaktiv gefragt,
                           sonst default. min=schnell, max=kleinste Datei (langsam)

INTERACTION
  --no-prompt              keine Rueckfragen (Backup=ja, DB-Backup=ja, Update=ja,
                           Service-Fehler=Abbruch -- modifizierbar via --no-files-backup,
                           --no-db-backup, --ignore-service-errors)
  --detach                 in den Hintergrund forken; nur PID + Logfile-Pfad
                           auf stderr, Parent exit 0 (setzt --no-prompt voraus)

LOGGING
  --logfile <path>         eigener Logfile-Pfad (Default: <TempDir>/updater-<ts>.log)

LOCALE / OUTPUT
  --lang <de|en|fr>        UI-Sprache erzwingen (Default: aus LC_ALL / LC_MESSAGES / LANG)
  --json                   NDJSON-Events auf stdout statt lokalisierter Ausgabe
                           (setzt --no-prompt voraus; Logfile bekommt ebenfalls JSON)

DRY-RUN-CHECKS
  --dry-run                fuehrt Pre-Flight-Checks aus (ohne Mutation):
                             backup_dir_writable      (fatal)
                             service_stop_binary      (fatal, skipped mit --skip-service)
                             service_start_binary     (fatal, skipped mit --skip-service)
                             service_running          (informational, via launchctl/
                                                       systemctl/sc; unklar => ok)
                             pgdump_binary            (fatal, skipped mit --no-db-backup)
                             pgdump_conn_host         (informational)
                             pgdump_conn_database     (fatal)
                             pgdump_conn_user         (fatal)
                             pgdump_conn_password     (fatal, kein Wert geloggt)
                             pgdump_connectivity      (informational, via pg_isready)
                             download_url             (fatal, skipped mit --zip)
                           Exit 0 = alle fatalen ok | Exit 9 = mindestens einer failed
                           Bei --zip wird statt URL-Probe der lokale ZIP-Pfad geprueft
                           und zusaetzlich Extract + Diff gezeigt.

MISC
  --version                Version anzeigen / show version
  --help                   diese Hilfe / this help

EXAMPLES
  Default-Workflow (interaktiv, mit Prompts):
    updater

  CI / Automation, lokale ZIP, ohne Backups:
    updater --no-prompt --zip /tmp/release.zip \
            --no-files-backup --no-db-backup

  Self-Update aus Java-Service (detached, eigenes Logfile):
    updater --detach --no-prompt \
            --logfile /var/log/myapp/updater.log \
            --zip /tmp/release.zip \
            --ignore-service-errors

  Java/CI-Aufruf mit maschinenlesbarer NDJSON-Ausgabe:
    updater --json --no-prompt --zip /tmp/release.zip

  Pre-Flight-Check ohne Mutation (Download nur kurz angetestet):
    updater --dry-run
`
	fmt.Fprint(w, help)
}

func resolveAppRoot(override string) (string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("--app-root: %w", err)
		}
		return abs, nil
	}
	return paths.AppRoot()
}

// acquireZip resolves the source ZIP via --zip or by downloading.
// Returns the local path, a cleanup function (always non-nil), and an exit code.
func acquireZip(f *flagSet, cfg *config.Config, emit *machine.Emitter, stderr io.Writer, s i18n.Strings) (string, func(), int) {
	if f.zipPath != "" {
		abs, err := filepath.Abs(f.zipPath)
		if err != nil {
			fmt.Fprintln(stderr, s.PathError, err)
			emit.FatalError("zip_path", err.Error())
			return "", noopCleanup, exitConfig
		}
		if _, err := os.Stat(abs); err != nil {
			fmt.Fprintln(stderr, s.ZipNotFound, err)
			emit.FatalError("zip_not_found", err.Error())
			return "", noopCleanup, exitConfig
		}
		fmt.Fprintln(stderr, s.UsingLocalZip, abs)
		return abs, noopCleanup, exitOK
	}

	client, err := download.NewClient(
		time.Duration(cfg.DownloadTimeoutSecs)*time.Second,
		download.ProxyConfig{
			URL: cfg.ProxyURL, User: cfg.ProxyUser,
			Password: cfg.ProxyPassword, NoProxy: cfg.ProxyNoProxy,
		},
	)
	if err != nil {
		fmt.Fprintln(stderr, s.HTTPClientError, err)
		emit.FatalError("http_client", err.Error())
		return "", noopCleanup, exitConfig
	}

	tmp, err := os.CreateTemp("", "updater-*.zip")
	if err != nil {
		fmt.Fprintln(stderr, s.TempFileError, err)
		emit.FatalError("temp_file", err.Error())
		return "", noopCleanup, exitDownload
	}
	dest := tmp.Name()
	tmp.Close()
	cleanup := func() { _ = os.Remove(dest) }

	d := &download.Downloader{Client: client, Progress: stderr}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DownloadTimeoutSecs)*time.Second)
	defer cancel()

	fmt.Fprintln(stderr, s.DownloadStart, cfg.DownloadURL)
	emit.DownloadStart(cfg.DownloadURL)
	n, err := d.Download(ctx, cfg.DownloadURL, dest)
	if err != nil {
		fmt.Fprintln(stderr, s.DownloadFailed, err)
		fmt.Fprintln(stderr, s.DownloadHint)
		emit.DownloadFailed(err.Error())
		cleanup()
		return "", noopCleanup, exitDownload
	}
	emit.DownloadDone(n)
	return dest, cleanup, exitOK
}

func noopCleanup() {}

// resolveLang picks the UI language. If the CLI override is non-empty it
// must match de|en|fr (case-insensitive). Empty falls back to env-based
// detection. The boolean is false if the override was set but invalid, so
// the caller can surface a clean error.
func resolveLang(override string) (i18n.Lang, bool) {
	if strings.TrimSpace(override) == "" {
		return i18n.Detect(), true
	}
	return i18n.ParseLang(override)
}

// resolvePgDumpBinary returns the pg_dump binary to invoke. The conf-supplied
// path wins; otherwise we fall back to a PATH lookup. Returns "" when neither
// resolves so the caller can print the "skipped" message and continue.
func resolvePgDumpBinary(confPath string) string {
	if confPath != "" {
		return confPath
	}
	if p, err := exec.LookPath("pg_dump"); err == nil {
		return p
	}
	return ""
}

// buildPgDumpEnv maps the pgdump.{host,port,user,password,db} conf keys to
// their libpq equivalents and returns them as KEY=VAL strings ready to be
// appended to os.Environ() inside RunPgDump. Empty values are skipped so the
// parent process environment / .pgpass keep their precedence for unset keys.
func buildPgDumpEnv(cfg *config.Config) []string {
	var env []string
	if cfg.PgdumpHost != "" {
		env = append(env, "PGHOST="+cfg.PgdumpHost)
	}
	if cfg.PgdumpPort != "" {
		env = append(env, "PGPORT="+cfg.PgdumpPort)
	}
	if cfg.PgdumpUser != "" {
		env = append(env, "PGUSER="+cfg.PgdumpUser)
	}
	if cfg.PgdumpPassword != "" {
		env = append(env, "PGPASSWORD="+cfg.PgdumpPassword)
	}
	if cfg.PgdumpDB != "" {
		env = append(env, "PGDATABASE="+cfg.PgdumpDB)
	}
	return env
}
