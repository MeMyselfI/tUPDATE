package archive

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func writeFileMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// archiveEntry is a decoded tar member from a .tar.xz backup.
type archiveEntry struct {
	mode os.FileMode
	body string
}

// readBackup decompresses a .tar.xz backup and returns its regular-file
// members keyed by slash-path name (directories excluded).
func readBackup(t *testing.T, path string) map[string]archiveEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	xr, err := xz.NewReader(f)
	if err != nil {
		t.Fatalf("xz reader: %v", err)
	}
	tr := tar.NewReader(xr)

	out := make(map[string]archiveEntry)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = archiveEntry{mode: hdr.FileInfo().Mode().Perm(), body: string(body)}
	}
	return out
}

func TestBackupDirs_BasicTree(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")

	writeFileMode(t, filepath.Join(app, "bin", "run.sh"), "#!/bin/sh\nrun\n", 0o755)
	writeFileMode(t, filepath.Join(app, "etc", "config.txt"), "k=v\n", 0o644)
	writeFileMode(t, filepath.Join(app, "etc", "sub", "deep.txt"), "deep", 0o644)

	ts := time.Date(2026, 6, 16, 14, 32, 5, 0, time.UTC)
	outPath, err := BackupDirs(app, backup, []string{"bin", "etc"}, ts, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	wantName := "2026-06-16-14-32-05.tar.xz"
	if filepath.Base(outPath) != wantName {
		t.Errorf("archive name = %q, want %q", filepath.Base(outPath), wantName)
	}

	entries := readBackup(t, outPath)
	var names []string
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"bin/run.sh", "etc/config.txt", "etc/sub/deep.txt"}
	if !equalStringSlice(names, want) {
		t.Errorf("entries = %v, want %v", names, want)
	}
}

func TestBackupDirs_PreservesUnixModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits irrelevant on Windows")
	}
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "exec"), "x", 0o755)
	writeFileMode(t, filepath.Join(app, "bin", "ro"), "y", 0o400)

	outPath, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	entries := readBackup(t, outPath)
	if entries["bin/exec"].mode != 0o755 {
		t.Errorf("bin/exec mode = %o, want 0755", entries["bin/exec"].mode)
	}
	if entries["bin/ro"].mode != 0o400 {
		t.Errorf("bin/ro mode = %o, want 0400", entries["bin/ro"].mode)
	}
}

func TestBackupDirs_RoundTripContents(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "libs", "foo.jar"), "JAR-BODY", 0o644)

	outPath, err := BackupDirs(app, backup, []string{"libs"}, time.Now(), nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	entries := readBackup(t, outPath)
	e, ok := entries["libs/foo.jar"]
	if !ok {
		t.Fatal("libs/foo.jar not found in backup")
	}
	if e.body != "JAR-BODY" {
		t.Errorf("body = %q", e.body)
	}
}

func TestBackupDirs_ReportsProgress(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "a"), strings.Repeat("a", 1000), 0o644)
	writeFileMode(t, filepath.Join(app, "bin", "b"), strings.Repeat("b", 2000), 0o644)

	var lastDone, lastTotal int64
	calls := 0
	_, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), func(done, total int64) {
		calls++
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if calls == 0 {
		t.Fatal("progress never called")
	}
	if lastTotal != 3000 {
		t.Errorf("total = %d, want 3000", lastTotal)
	}
	if lastDone != 3000 {
		t.Errorf("final done = %d, want 3000", lastDone)
	}
}

func TestBackupDirs_MissingDirSkipped(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "x"), "y", 0o644)

	// "www" does not exist - should be skipped without error.
	outPath, err := BackupDirs(app, backup, []string{"bin", "www"}, time.Now(), nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
}

func TestBackupDirs_EmptyDirSetCreatesEmptyArchive(t *testing.T) {
	tmp := t.TempDir()
	outPath, err := BackupDirs(tmp, filepath.Join(tmp, "backup"), []string{"none"}, time.Now(), nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
}

func TestBackupDirs_FilenameFormat(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "x"), "y", 0o644)

	ts := time.Date(2025, 1, 9, 8, 7, 6, 0, time.UTC)
	outPath, err := BackupDirs(app, backup, []string{"bin"}, ts, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if filepath.Base(outPath) != "2025-01-09-08-07-06.tar.xz" {
		t.Errorf("filename = %q", filepath.Base(outPath))
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
