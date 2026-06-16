package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// version is overridden at build time via -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	logPath, logFile, err := openLogFile(time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Logfile-Fehler:", err)
		os.Exit(1)
	}
	defer logFile.Close()

	fmt.Fprintln(os.Stderr, "Logfile:", logPath)
	fmt.Fprintf(logFile, "=== updater %s gestartet: %s ===\n", version, time.Now().Format(time.RFC3339))

	stdout := io.MultiWriter(os.Stdout, logFile)
	stderr := io.MultiWriter(os.Stderr, logFile)

	code := runApp(os.Stdin, stdout, stderr, os.Args[1:], version)

	fmt.Fprintf(logFile, "=== updater beendet: exit=%d, %s ===\n", code, time.Now().Format(time.RFC3339))
	os.Exit(code)
}

// openLogFile creates an updater-<timestamp>.log file in the OS temp directory
// and returns its absolute path and the open file handle.
func openLogFile(ts time.Time) (string, *os.File, error) {
	name := fmt.Sprintf("updater-%s.log", ts.Format("2006-01-02-15-04-05"))
	path := filepath.Join(os.TempDir(), name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, err
	}
	return path, f, nil
}
