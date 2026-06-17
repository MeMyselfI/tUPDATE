package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveLogPath_DefaultsToTempDir(t *testing.T) {
	ts := time.Date(2026, 6, 17, 10, 20, 30, 0, time.UTC)
	got, err := resolveLogPath("", ts)
	if err != nil {
		t.Fatalf("resolveLogPath: %v", err)
	}
	wantSuffix := "updater-2026-06-17-10-20-30.log"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("path %q should end in %q", got, wantSuffix)
	}
	if !strings.HasPrefix(got, os.TempDir()) {
		t.Errorf("path %q should live under TempDir %q", got, os.TempDir())
	}
}

func TestResolveLogPath_AbsolutePathPassthrough(t *testing.T) {
	got, err := resolveLogPath("/var/log/foo.log", time.Now())
	if err != nil {
		t.Fatalf("resolveLogPath: %v", err)
	}
	if got != "/var/log/foo.log" {
		t.Errorf("absolute path should pass through, got %q", got)
	}
}

func TestResolveLogPath_RelativeResolvedToCwd(t *testing.T) {
	cwd, _ := os.Getwd()
	got, err := resolveLogPath("logs/x.log", time.Now())
	if err != nil {
		t.Fatalf("resolveLogPath: %v", err)
	}
	want := filepath.Join(cwd, "logs", "x.log")
	if got != want {
		t.Errorf("relative resolution: got %q, want %q", got, want)
	}
}

func TestOpenLogFile_CreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "deep", "nested", "dir", "run.log")
	path, f, err := openLogFile(target, time.Now())
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer f.Close()
	if path != target {
		t.Errorf("path = %q, want %q", path, target)
	}
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestOpenLogFile_TruncatesExistingFile(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "run.log")
	if err := os.WriteFile(target, []byte("old content from previous run"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, f, err := openLogFile(target, time.Now())
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer f.Close()
	if path != target {
		t.Errorf("path = %q, want %q", path, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("expected truncated file, size = %d", info.Size())
	}
}

func TestBuildDetachChildArgs_StripsDetachInjectsLogfile(t *testing.T) {
	in := []string{"--no-prompt", "--detach", "--zip", "/tmp/r.zip", "--no-files-backup"}
	out := buildDetachChildArgs(in, "/var/log/u.log")
	for _, a := range out {
		if a == "--detach" {
			t.Errorf("--detach should be stripped, got %v", out)
		}
	}
	// Should end with --logfile <path>
	if len(out) < 2 || out[len(out)-2] != "--logfile" || out[len(out)-1] != "/var/log/u.log" {
		t.Errorf("expected ... --logfile /var/log/u.log at end, got %v", out)
	}
}

func TestBuildDetachChildArgs_OverridesUserLogfile(t *testing.T) {
	in := []string{"--no-prompt", "--detach", "--logfile", "/wrong/path.log", "--zip", "/tmp/r.zip"}
	out := buildDetachChildArgs(in, "/resolved/abs.log")
	for i, a := range out {
		if a == "--logfile" && i+1 < len(out) && out[i+1] == "/wrong/path.log" {
			t.Errorf("user --logfile path leaked into child args: %v", out)
		}
	}
	if out[len(out)-2] != "--logfile" || out[len(out)-1] != "/resolved/abs.log" {
		t.Errorf("expected resolved --logfile at end, got %v", out)
	}
}

func TestBuildDetachChildArgs_HandlesEqualsForm(t *testing.T) {
	in := []string{"--no-prompt", "--detach=true", "--logfile=/wrong/x.log", "--zip=/tmp/r.zip"}
	out := buildDetachChildArgs(in, "/right.log")
	for _, a := range out {
		if strings.HasPrefix(a, "--detach") {
			t.Errorf("--detach= form should be stripped, got %v", out)
		}
		if a == "--logfile=/wrong/x.log" {
			t.Errorf("--logfile= form should be stripped, got %v", out)
		}
	}
	if out[len(out)-2] != "--logfile" || out[len(out)-1] != "/right.log" {
		t.Errorf("expected --logfile /right.log at end, got %v", out)
	}
}

func TestWriteHelp_ContainsAllSectionsAndExamples(t *testing.T) {
	var buf strings.Builder
	writeHelp(&buf)
	out := buf.String()

	for _, want := range []string{
		"USAGE",
		"INPUT / RUN MODE",
		"SERVICE",
		"BACKUP",
		"INTERACTION",
		"LOGGING",
		"MISC",
		"EXAMPLES",
		"--no-files-backup",
		"--no-db-backup",
		"--ignore-service-errors",
		"--detach",
		"--logfile",
		"updater --no-prompt --zip /tmp/release.zip",
		"--detach --no-prompt",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n--- full ---\n%s", want, out)
		}
	}
}
