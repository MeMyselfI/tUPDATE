package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"updater/internal/archive"
	"updater/internal/config"
	"updater/internal/download"
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

// flags holds parsed command-line options.
type flagSet struct {
	zipPath     string
	configPath  string
	appRoot     string // optional override of dirname(executable)
	dryRun      bool
	noPrompt    bool
	skipService bool
	showVersion bool
}

// runApp executes the updater workflow.
// Returns one of the exit* constants.
func runApp(stdin io.Reader, stdout, stderr io.Writer, args []string, version string) int {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	if f.showVersion {
		fmt.Fprintf(stdout, "tOSCE-Updater %s\n", version)
		return exitOK
	}

	appRoot, err := resolveAppRoot(f.appRoot)
	if err != nil {
		fmt.Fprintln(stderr, "Fehler:", err)
		return exitConfig
	}

	configPath := f.configPath
	if configPath == "" {
		configPath = filepath.Join(appRoot, "conf", "updater.properties")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, "Config-Fehler:", err)
		return exitConfig
	}

	stopCmd := cfg.StopCommand(runtime.GOOS)
	startCmd := cfg.StartCommand(runtime.GOOS)
	if !f.skipService && (stopCmd == "" || startCmd == "") {
		fmt.Fprintf(stderr, "Keine Service-Kommandos für GOOS=%s konfiguriert.\n", runtime.GOOS)
		return exitConfig
	}

	zipFile, cleanupZip, exitCode := acquireZip(f, cfg, stderr)
	if exitCode != exitOK {
		return exitCode
	}
	defer cleanupZip()

	tempDir, err := os.MkdirTemp("", "updater-extract-*")
	if err != nil {
		fmt.Fprintln(stderr, "Temp-Dir-Fehler:", err)
		return exitExtract
	}
	defer os.RemoveAll(tempDir)

	if err := archive.Extract(zipFile, tempDir); err != nil {
		fmt.Fprintln(stderr, "Extract-Fehler:", err)
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
		fmt.Fprintln(stderr, "Service starten:", startCmd)
		startCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		defer cancel()
		if err := runner.Run(startCtx, startCmd, time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second); err != nil {
			fmt.Fprintln(stderr, "Service-Start-Fehler:", err)
		}
	}

	if !f.skipService {
		fmt.Fprintln(stderr, "Service stoppen:", stopCmd)
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		err := runner.Run(stopCtx, stopCmd, time.Duration(cfg.ServiceStopTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, "Service-Stop-Fehler:", err)
			return exitServiceStop
		}
		serviceWasStopped = true
	}

	fmt.Fprintln(stderr, "Diff berechnen...")
	diffs, err := sync.Compute(tempDir, appRoot, cfg.SyncDirectories)
	if err != nil {
		fmt.Fprintln(stderr, "Diff-Fehler:", err)
		maybeStartService()
		return exitSync
	}

	fmt.Fprint(stdout, sync.FormatReport(diffs))
	summary := sync.Summarize(diffs)

	if f.dryRun {
		fmt.Fprintln(stderr, "Dry-Run beendet, keine Änderungen.")
		maybeStartService()
		return exitOK
	}
	if !summary.HasChanges() {
		fmt.Fprintln(stderr, "Keine Änderungen.")
		maybeStartService()
		return exitOK
	}

	var prompter prompt.Prompter
	if f.noPrompt {
		prompter = prompt.Always{Answer: true}
	} else {
		prompter = &prompt.Stdin{In: stdin, Out: stdout, MaxAttempts: 3}
	}

	wantBackup, err := prompter.Confirm("Backup der aktuellen Verzeichnisse erstellen?", false)
	if err != nil {
		fmt.Fprintln(stderr, "Prompt-Fehler:", err)
		maybeStartService()
		return exitUserAbort
	}

	var backupPath string
	if wantBackup {
		fmt.Fprintln(stderr, "Backup wird erstellt...")
		backupDir := filepath.Join(appRoot, cfg.BackupDirectory)
		p, err := archive.BackupDirs(appRoot, backupDir, cfg.SyncDirectories, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, "Backup-Fehler:", err)
			maybeStartService()
			return exitSync
		}
		backupPath = p
		fmt.Fprintln(stderr, "Backup:", backupPath)
	}

	wantUpdate, err := prompter.Confirm("Update jetzt durchführen?", false)
	if err != nil {
		fmt.Fprintln(stderr, "Prompt-Fehler:", err)
		maybeStartService()
		return exitUserAbort
	}
	if !wantUpdate {
		fmt.Fprintln(stderr, "Update vom Benutzer abgebrochen.")
		maybeStartService()
		return exitUserAbort
	}

	fmt.Fprintln(stderr, "Update wird angewendet...")
	if err := sync.Apply(tempDir, appRoot, diffs); err != nil {
		fmt.Fprintln(stderr, "Sync-Fehler:", err)
		if backupPath != "" {
			fmt.Fprintln(stderr, "Backup zum Wiederherstellen:", backupPath)
		}
		maybeStartService()
		return exitSync
	}
	fmt.Fprintln(stderr, "Update erfolgreich.")

	if !f.skipService {
		fmt.Fprintln(stderr, "Service starten:", startCmd)
		startCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		err := runner.Run(startCtx, startCmd, time.Duration(cfg.ServiceStartTimeoutSecs)*time.Second)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, "Service-Start-Fehler:", err)
			return exitServiceStart
		}
	}

	fmt.Fprintln(stderr, "Fertig.")
	return exitOK
}

func parseFlags(args []string, stderr io.Writer) (*flagSet, error) {
	fs := flag.NewFlagSet("updater", flag.ContinueOnError)
	fs.SetOutput(stderr)

	f := &flagSet{}
	fs.StringVar(&f.zipPath, "zip", "", "lokale ZIP statt Download nutzen")
	fs.StringVar(&f.configPath, "config", "", "Pfad zu updater.properties (Default: <approot>/conf/updater.properties)")
	fs.StringVar(&f.appRoot, "app-root", "", "App-Root überschreiben (Default: dirname(executable))")
	fs.BoolVar(&f.dryRun, "dry-run", false, "nur Diff anzeigen, nichts ändern")
	fs.BoolVar(&f.noPrompt, "no-prompt", false, "keine Rückfragen (default: ja)")
	fs.BoolVar(&f.skipService, "skip-service", false, "Service nicht stoppen/starten")
	fs.BoolVar(&f.showVersion, "version", false, "Version anzeigen")

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
func acquireZip(f *flagSet, cfg *config.Config, stderr io.Writer) (string, func(), int) {
	if f.zipPath != "" {
		abs, err := filepath.Abs(f.zipPath)
		if err != nil {
			fmt.Fprintln(stderr, "Pfad-Fehler:", err)
			return "", noopCleanup, exitConfig
		}
		if _, err := os.Stat(abs); err != nil {
			fmt.Fprintln(stderr, "ZIP nicht gefunden:", err)
			return "", noopCleanup, exitConfig
		}
		fmt.Fprintln(stderr, "Verwende lokale ZIP:", abs)
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
		fmt.Fprintln(stderr, "HTTP-Client-Fehler:", err)
		return "", noopCleanup, exitConfig
	}

	tmp, err := os.CreateTemp("", "updater-*.zip")
	if err != nil {
		fmt.Fprintln(stderr, "Temp-Datei-Fehler:", err)
		return "", noopCleanup, exitDownload
	}
	dest := tmp.Name()
	tmp.Close()
	cleanup := func() { _ = os.Remove(dest) }

	d := &download.Downloader{Client: client, Progress: stderr}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.DownloadTimeoutSecs)*time.Second)
	defer cancel()

	fmt.Fprintln(stderr, "Download:", cfg.DownloadURL)
	if _, err := d.Download(ctx, cfg.DownloadURL, dest); err != nil {
		fmt.Fprintln(stderr, "Download fehlgeschlagen:", err)
		fmt.Fprintln(stderr, "Tipp: --zip <path> für lokale Datei nutzen.")
		cleanup()
		return "", noopCleanup, exitDownload
	}
	return dest, cleanup, exitOK
}

func noopCleanup() {}
