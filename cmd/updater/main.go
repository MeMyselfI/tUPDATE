package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"updater/internal/i18n"
	"updater/internal/machine"
)

// version is overridden at build time via -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	// Pre-parse to learn about --detach, --logfile, --json, --lang BEFORE
	// opening the log or printing localized text.
	f, err := parseFlags(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if f.showVersion {
		fmt.Fprintf(os.Stdout, "tUPDATE %s\n", version)
		os.Exit(0)
	}

	lang, ok := resolveLang(f.lang)
	if !ok {
		fmt.Fprintf(os.Stderr, "--lang: invalid value %q, expected de|en|fr\n", f.lang)
		os.Exit(1)
	}
	s := i18n.Get(lang)

	if f.detach {
		exitDetachParent(s, f)
		return
	}

	logPath, logFile, err := openLogFile(f.logfile, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, s.LogfileError, err)
		os.Exit(1)
	}
	defer logFile.Close()

	var stdout, stderr, emitWriter io.Writer
	if f.jsonOut {
		// JSON-only mode: stdout + logfile receive ONLY NDJSON events.
		// Localized human writes get sent to io.Discard so the writes that
		// pepper runApp don't pollute the stream.
		emitWriter = io.MultiWriter(os.Stdout, logFile)
		stdout = io.Discard
		stderr = io.Discard
		machine.New(emitWriter, true).Ready(version, logPath)
	} else {
		fmt.Fprintln(os.Stderr, s.BetaWarning)
		fmt.Fprintln(os.Stderr, s.LogfileLabel, logPath)
		fmt.Fprintf(logFile, s.StartedMarker+"\n", version, time.Now().Format(time.RFC3339))
		fmt.Fprintln(logFile, s.BetaWarning)
		stdout = io.MultiWriter(os.Stdout, logFile)
		stderr = io.MultiWriter(os.Stderr, logFile)
		emitWriter = io.Discard
	}

	code := runApp(os.Stdin, stdout, stderr, emitWriter, os.Args[1:], version)

	if !f.jsonOut {
		fmt.Fprintf(logFile, s.EndedMarker+"\n", code, time.Now().Format(time.RFC3339))
	}

	// Double-clicked on Windows the console window is ours alone and vanishes
	// on exit, so a clean run would flash by unread. Count down first, and let
	// Enter cut it short. Failures skip this: the window stays up so the error
	// remains readable. The countdown goes to os.Stderr (the real console),
	// never the MultiWriter, so its \r frames stay out of the logfile.
	if code == exitOK && !f.jsonOut && !f.detach && isTerminal(os.Stderr) && ownsConsole() {
		waitBeforeClose(os.Stderr, os.Stdin, f.closeDelay, s.ConsoleClosing)
	}

	os.Exit(code)
}

// exitDetachParent runs the parent half of --detach: validate, resolve the
// logfile path, spawn the detached child, print the result line, exit 0.
//
// The parent intentionally does NOT print the beta banner — that belongs in
// the child's logfile only, so the operator only sees one tidy line:
//
//	Updater detached, PID=<n>, logfile=<path>
func exitDetachParent(s i18n.Strings, f *flagSet) {
	if !f.noPrompt {
		fmt.Fprintln(os.Stderr, "--detach requires --no-prompt")
		os.Exit(1)
	}

	logPath, err := resolveLogPath(f.logfile, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, s.LogfileError, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, s.LogfileError, err)
		os.Exit(1)
	}

	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "executable lookup:", err)
		os.Exit(1)
	}

	childArgs := buildDetachChildArgs(os.Args[1:], logPath)

	pid, err := spawnDetached(binary, childArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "detach failed:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Updater detached, PID=%d, logfile=%s\n", pid, logPath)
	os.Exit(0)
}

// buildDetachChildArgs removes --detach (and its short form) from the parent
// args, drops any user-supplied --logfile pair, then appends a fresh
// --logfile <resolved-path> so the child always opens exactly the path the
// parent printed to the operator.
func buildDetachChildArgs(parentArgs []string, logPath string) []string {
	out := make([]string, 0, len(parentArgs)+2)
	skipNext := false
	for _, a := range parentArgs {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case a == "--detach" || a == "-detach":
			continue
		case a == "--logfile" || a == "-logfile":
			skipNext = true
			continue
		case strings.HasPrefix(a, "--logfile=") || strings.HasPrefix(a, "-logfile="):
			continue
		case strings.HasPrefix(a, "--detach=") || strings.HasPrefix(a, "-detach="):
			continue
		}
		out = append(out, a)
	}
	out = append(out, "--logfile", logPath)
	return out
}

// resolveLogPath returns the absolute path the run should log to. An explicit
// user-supplied path wins; otherwise a deterministic <TempDir>/updater-<ts>.log
// is used. Relative paths are resolved against the current working directory.
func resolveLogPath(userPath string, ts time.Time) (string, error) {
	if strings.TrimSpace(userPath) != "" {
		abs, err := filepath.Abs(userPath)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	name := fmt.Sprintf("updater-%s.log", ts.Format("2006-01-02-15-04-05"))
	return filepath.Join(os.TempDir(), name), nil
}

// openLogFile opens the logfile for this run. Parent directories are created
// if necessary; the file is truncated so every run starts fresh and rotation
// is the operator's responsibility.
func openLogFile(userPath string, ts time.Time) (string, *os.File, error) {
	path, err := resolveLogPath(userPath, ts)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil, err
	}
	f, err := openLogShared(path)
	if err != nil {
		return "", nil, err
	}
	return path, f, nil
}
