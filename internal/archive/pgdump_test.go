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

// fakePgDumpRecording writes a small shell script that:
//   * records argv to argvPath (when non-empty)
//   * records the PG* env vars to envPath (when non-empty)
//   * writes a dummy dump payload to the -f target
//   * exits with the given code
func fakePgDumpRecording(t *testing.T, argvPath, envPath string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "pg_dump.bat")
		body := "@echo off\r\n"
		if argvPath != "" {
			body += "echo %* > \"" + argvPath + "\"\r\n"
		}
		if envPath != "" {
			body += "set > \"" + envPath + "\"\r\n"
		}
		body += "exit /b " + itoa(exitCode) + "\r\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatalf("write fake pg_dump: %v", err)
		}
		return script
	}
	script := filepath.Join(dir, "pg_dump")
	body := "#!/bin/sh\n"
	if argvPath != "" {
		body += `echo "$@" > "` + argvPath + `"` + "\n"
	}
	if envPath != "" {
		body += `env | grep -E '^PG' > "` + envPath + `" 2>/dev/null || true` + "\n"
	}
	body += `out=""
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
	bin := fakePgDumpRecording(t, recordPath, "", 0)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), PgDumpOptions{
		Binary:    bin,
		OutFile:   outFile,
		ExtraArgs: []string{"-h", "localhost", "mydb"},
	}, &log)
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
	bin := fakePgDumpRecording(t, recordPath, "", 1)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), PgDumpOptions{
		Binary:  bin,
		OutFile: outFile,
	}, &log)
	if err == nil {
		t.Fatal("expected RunPgDump to return an error on non-zero exit")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Errorf("expected outFile to be cleaned up, stat err = %v", err)
	}
}

func TestRunPgDump_EmptyBinaryRejected(t *testing.T) {
	err := RunPgDump(context.Background(), PgDumpOptions{Binary: "", OutFile: "/tmp/x"}, nil)
	if err == nil {
		t.Fatal("expected error for empty binary path")
	}
}

func TestRunPgDump_EmptyOutFileRejected(t *testing.T) {
	err := RunPgDump(context.Background(), PgDumpOptions{Binary: "/usr/bin/true", OutFile: ""}, nil)
	if err == nil {
		t.Fatal("expected error for empty output file path")
	}
}

func TestRunPgDump_ExtraEnvInjected(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env.log")
	bin := fakePgDumpRecording(t, "", envPath, 0)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), PgDumpOptions{
		Binary:  bin,
		OutFile: outFile,
		ExtraEnv: []string{
			"PGHOST=db.example.org",
			"PGPORT=5433",
			"PGUSER=alice",
			"PGPASSWORD=s3cret",
			"PGDATABASE=app",
		},
	}, &log)
	if err != nil {
		t.Fatalf("RunPgDump failed: %v", err)
	}
	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env log: %v", err)
	}
	env := string(envBytes)
	for _, want := range []string{
		"PGHOST=db.example.org",
		"PGPORT=5433",
		"PGUSER=alice",
		"PGPASSWORD=s3cret",
		"PGDATABASE=app",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q\nfull env:\n%s", want, env)
		}
	}
}

func TestRunPgDump_AutoNoPasswordWhenHasPassword(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "argv.log")
	bin := fakePgDumpRecording(t, recordPath, "", 0)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), PgDumpOptions{
		Binary:      bin,
		OutFile:     outFile,
		HasPassword: true,
	}, &log)
	if err != nil {
		t.Fatalf("RunPgDump failed: %v", err)
	}
	argv, _ := os.ReadFile(recordPath)
	if !strings.Contains(string(argv), " -w ") && !strings.HasSuffix(strings.TrimSpace(string(argv)), " -w") {
		t.Errorf("expected -w in argv when HasPassword=true, got %q", string(argv))
	}
}

func TestRunPgDump_NoDuplicateW(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "argv.log")
	bin := fakePgDumpRecording(t, recordPath, "", 0)

	outFile := filepath.Join(dir, "dump.backup")
	var log bytes.Buffer

	err := RunPgDump(context.Background(), PgDumpOptions{
		Binary:      bin,
		OutFile:     outFile,
		HasPassword: true,
		ExtraArgs:   []string{"-w", "mydb"},
	}, &log)
	if err != nil {
		t.Fatalf("RunPgDump failed: %v", err)
	}
	argv, _ := os.ReadFile(recordPath)
	count := strings.Count(string(argv), "-w")
	if count != 1 {
		t.Errorf("expected exactly one -w, got %d in %q", count, string(argv))
	}
}
