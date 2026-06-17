package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"updater/internal/archive"
	"updater/internal/config"
	"updater/internal/download"
	"updater/internal/i18n"
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
)

// flagSet holds parsed command-line options.
type flagSet struct {
	zipPath     string
	configPath  string
	appRoot     string
	dryRun      bool
	noPrompt    bool
	skipService bool
	showVersion bool
}

// runApp executes the updater workflow.
// Returns one of the exit* constants.
func runApp(stdin io.Reader, stdout, stderr io.Writer, args []string, version string) int {
	s := i18n.Get(i18n.Detect())

	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	if f.showVersion {
		fmt.Fprintf(stdout, "tUPDATE %s\n", version)
		return exitOK
	}

	appRoot, err := resolveAppRoot(f.appRoot)
	if err != nil {
		fmt.Fprintln(stderr, s.ConfigError, err)
		return exitConfig
	}

	configPath := f.configPath
	if configPath == "" {
		configPath = filepath.Join(appRoot, "conf", "updater.properties")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, s.ConfigError, err)
		return exitConfig
	}

	stopCmd := cfg.StopCommand(runtime.GOOS)
	startCmd := cfg.StartCommand(runtime.GOOS)
	if !f.skipService && (stopCmd == "" || startCmd == "") {
		fmt.Fprintf(stderr, s.NoServiceCommandConfig+"\n", runtime.GOOS)
		return exitConfig
	}

	prompter := newPrompter(stdin, stdout, f.noPrompt, s)

	zipFile, cleanupZip, exitCode := acquireZip(f, cfg, stderr, s)
	if exitCode != exitOK {
		return exitCode
	}
	defer cleanupZip()

	tempDir, err := os.MkdirTemp("", "updater-extract-*")
	if err != nil {
		fmt.Fprintln(stderr, s.TempDirError, err)
		return exitExtract
	}
	defer os.RemoveAll(tempDir)

	if err := archive.Extract(zipFile, tempDir); err != nil {
		fmt.Fprintln(stderr, s.ExtractError, err)
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

	if !f.skipService {
		fmt.Fprintln(stderr, s.ServiceStopping, stopCmd)
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		err := runner.Run(stopCtx, stopCmd, time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, s.ServiceStopError, err)
			// In automation mode (--no-prompt) we bail immediately.
			// Interactively we ask the user whether to proceed without a stopped service.
			cont, perr := promptContinue(prompter, f.noPrompt, s, stderr)
			if perr != nil || !cont {
				return exitServiceStop
			}
			// User chose to continue — service was NOT stopped, so we must not
			// try to "restart" it later.
			serviceWasStopped = false
		} else {
			serviceWasStopped = true
		}
	}

	refRoot := sync.ResolveRefRoot(tempDir, cfg.SyncDirectories)
	if refRoot != tempDir {
		fmt.Fprintf(stderr, s.WrapperDetected+"\n", filepath.Base(refRoot))
	}

	fmt.Fprintln(stderr, s.ComputingDiff)
	diffs, err := sync.Compute(refRoot, appRoot, cfg.SyncDirectories)
	if err != nil {
		fmt.Fprintln(stderr, s.DiffError, err)
		maybeStartService()
		return exitSync
	}

	fmt.Fprint(stdout, sync.FormatReport(diffs))
	summary := sync.Summarize(diffs)

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

	wantBackup, err := prompter.Confirm(s.BackupQuestion, false)
	if err != nil {
		fmt.Fprintln(stderr, s.PromptError, err)
		maybeStartService()
		return exitUserAbort
	}

	backupTs := time.Now()
	backupDir := filepath.Join(appRoot, cfg.BackupDirectory)

	var backupPath string
	if wantBackup {
		fmt.Fprintln(stderr, s.BackupCreating)
		p, err := archive.BackupDirs(appRoot, backupDir, cfg.SyncDirectories, backupTs)
		if err != nil {
			fmt.Fprintln(stderr, s.BackupError, err)
			maybeStartService()
			return exitSync
		}
		backupPath = p
		fmt.Fprintln(stderr, s.BackupLabel, backupPath)
	}

	wantDBBackup, err := prompter.Confirm(s.DBBackupQuestion, false)
	if err != nil {
		fmt.Fprintln(stderr, s.PromptError, err)
		maybeStartService()
		return exitUserAbort
	}
	if wantDBBackup {
		pgBin := resolvePgDumpBinary(cfg.PgdumpBinary(runtime.GOOS))
		if pgBin == "" {
			fmt.Fprintln(stderr, s.DBBackupSkipped)
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
			} else {
				fmt.Fprintln(stderr, s.DBBackupDone, dumpPath)
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
	if err := sync.Apply(refRoot, appRoot, diffs); err != nil {
		fmt.Fprintln(stderr, s.SyncError, err)
		if backupPath != "" {
			fmt.Fprintln(stderr, s.RestoreFromBackup, backupPath)
		}
		maybeStartService()
		return exitSync
	}
	fmt.Fprintln(stderr, s.UpdateSuccess)

	if !f.skipService {
		fmt.Fprintln(stderr, s.ServiceStarting, startCmd)
		startCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		err := runner.Run(startCtx, startCmd, time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, s.ServiceStartError, err)
			cont, perr := promptContinue(prompter, f.noPrompt, s, stderr)
			if perr != nil || !cont {
				return exitServiceStart
			}
		}
	}

	fmt.Fprintln(stderr, s.Done)
	return exitOK
}

// newPrompter wires an interactive Stdin prompter localised to s, or
// returns prompt.Always{true} when running with --no-prompt.
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
// In --no-prompt mode the answer is always "no" so automation aborts on
// service failures instead of silently swallowing them.
func promptContinue(p prompt.Prompter, noPrompt bool, s i18n.Strings, stderr io.Writer) (bool, error) {
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

func parseFlags(args []string, stderr io.Writer) (*flagSet, error) {
	fs := flag.NewFlagSet("updater", flag.ContinueOnError)
	fs.SetOutput(stderr)

	f := &flagSet{}
	fs.StringVar(&f.zipPath, "zip", "", "lokale ZIP statt Download nutzen / use a local ZIP instead of downloading")
	fs.StringVar(&f.configPath, "config", "", "Pfad zu updater.properties (Default: <approot>/conf/updater.properties)")
	fs.StringVar(&f.appRoot, "app-root", "", "App-Root überschreiben (Default: dirname(dirname(executable)))")
	fs.BoolVar(&f.dryRun, "dry-run", false, "nur Diff anzeigen, nichts ändern / show diff only")
	fs.BoolVar(&f.noPrompt, "no-prompt", false, "keine Rückfragen (Backup=ja, Update=ja, Service-Fehler=Abbruch)")
	fs.BoolVar(&f.skipService, "skip-service", false, "Service nicht stoppen/starten / skip service stop/start")
	fs.BoolVar(&f.showVersion, "version", false, "Version anzeigen / show version")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return f, nil
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
func acquireZip(f *flagSet, cfg *config.Config, stderr io.Writer, s i18n.Strings) (string, func(), int) {
	if f.zipPath != "" {
		abs, err := filepath.Abs(f.zipPath)
		if err != nil {
			fmt.Fprintln(stderr, s.PathError, err)
			return "", noopCleanup, exitConfig
		}
		if _, err := os.Stat(abs); err != nil {
			fmt.Fprintln(stderr, s.ZipNotFound, err)
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
		return "", noopCleanup, exitConfig
	}

	tmp, err := os.CreateTemp("", "updater-*.zip")
	if err != nil {
		fmt.Fprintln(stderr, s.TempFileError, err)
		return "", noopCleanup, exitDownload
	}
	dest := tmp.Name()
	tmp.Close()
	cleanup := func() { _ = os.Remove(dest) }

	d := &download.Downloader{Client: client, Progress: stderr}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DownloadTimeoutSecs)*time.Second)
	defer cancel()

	fmt.Fprintln(stderr, s.DownloadStart, cfg.DownloadURL)
	if _, err := d.Download(ctx, cfg.DownloadURL, dest); err != nil {
		fmt.Fprintln(stderr, s.DownloadFailed, err)
		fmt.Fprintln(stderr, s.DownloadHint)
		cleanup()
		return "", noopCleanup, exitDownload
	}
	return dest, cleanup, exitOK
}

func noopCleanup() {}

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
