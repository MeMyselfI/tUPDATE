package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakePgDump writes a small shell or batch script that records its argv to
// <recordPath> and either succeeds or fails depending on exitCode.
// Returns the absolute path to the script.
func fakePgDump(t *testing.T, recordPath string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		// Windows tests run rarely on this dev box, but keep the helper safe.
		script := filepath.Join(dir, "pg_dump.bat")
		body := "@echo off\r\n" +
			"echo %* > \"" + recordPath + "\"\r\n" +
			"exit /b " + itoa(exitCode) + "\r\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatalf("write fake pg_dump: %v", err)
		}
		return script
	}
	script := filepath.Join(dir, "pg_dump")
	// Echo the entire arg list, including the -f <file> pair, so the test can
	// assert ordering. Additionally, touch the output file the test expects so
	// downstream readers don't fail on a missing file.
	body := `#!/bin/sh
echo "$@" > "` + recordPath + `"
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-f" ]; then
    out="$a"
  fi
  prev="$a"
done
if [ -n "$out" ]; then
  echo dummy-dump-payload > "$out"
fi
exit ` + itoa(exitCode) + `
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	return script
}

func itoa(i int) string {
	switch {
	case i == 0:
		return "0"
	case i < 0:
		return "-" + itoa(-i)
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestPgDumpPath_UsesSameFormatAsBackup(t *testing.T) {
	ts := time.Date(2026, 6, 17, 10, 20, 30, 0, time.UTC)
	got := PgDumpPath("/tmp/backup", ts)
	want := filepath.Join("/tmp/backup", "2026-06-17-10-20-30-db.backup")
	if got != want {
		t.Errorf("PgDumpPath = %q, want %q", got, want)
	}
}

func TestRunPgDump_Success(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "argv.log")
	bin := fakePgDump(t, recordPath, 0)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), bin, outFile, []string{"-h", "localhost", "mydb"}, &log)
	if err != nil {
		t.Fatalf("RunPgDump failed: %v", err)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Errorf("output file not produced: %v", err)
	}
	argv, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	got := strings.TrimSpace(string(argv))
	if !strings.HasPrefix(got, "-Fc -f ") {
		t.Errorf("expected -Fc -f to come first, got %q", got)
	}
	if !strings.Contains(got, "-h localhost mydb") {
		t.Errorf("expected extra args appended after -f, got %q", got)
	}
	if !strings.Contains(got, outFile) {
		t.Errorf("expected outFile %q in argv, got %q", outFile, got)
	}
}

func TestRunPgDump_FailureRemovesOutput(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "argv.log")
	bin := fakePgDump(t, recordPath, 1)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), bin, outFile, nil, &log)
	if err == nil {
		t.Fatal("expected RunPgDump to return an error on non-zero exit")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Errorf("expected outFile to be cleaned up, stat err = %v", err)
	}
}

func TestRunPgDump_EmptyBinaryRejected(t *testing.T) {
	err := RunPgDump(context.Background(), "", "/tmp/x", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty binary path")
	}
}

func TestRunPgDump_EmptyOutFileRejected(t *testing.T) {
	err := RunPgDump(context.Background(), "/usr/bin/true", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty output file path")
	}
}
