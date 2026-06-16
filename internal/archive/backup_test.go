package archive

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
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

func listZip(t *testing.T, path string) []*zip.File {
	t.Helper()
	rc, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	t.Cleanup(func() { rc.Close() })
	return rc.File
}

func TestBackupDirs_BasicTree(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")

	writeFileMode(t, filepath.Join(app, "bin", "run.sh"), "#!/bin/sh\nrun\n", 0o755)
	writeFileMode(t, filepath.Join(app, "etc", "config.txt"), "k=v\n", 0o644)
	writeFileMode(t, filepath.Join(app, "etc", "sub", "deep.txt"), "deep", 0o644)

	ts := time.Date(2026, 6, 16, 14, 32, 5, 0, time.UTC)
	zipPath, err := BackupDirs(app, backup, []string{"bin", "etc"}, ts)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	wantName := "2026-06-16-14-32-05.zip"
	if filepath.Base(zipPath) != wantName {
		t.Errorf("zip name = %q, want %q", filepath.Base(zipPath), wantName)
	}

	files := listZip(t, zipPath)
	var names []string
	for _, f := range files {
		if !strings.HasSuffix(f.Name, "/") {
			names = append(names, f.Name)
		}
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

	zipPath, err := BackupDirs(app, backup, []string{"bin"}, time.Now())
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	files := listZip(t, zipPath)
	got := make(map[string]os.FileMode)
	for _, f := range files {
		got[f.Name] = f.Mode().Perm()
	}
	if got["bin/exec"] != 0o755 {
		t.Errorf("bin/exec mode = %o, want 0755", got["bin/exec"])
	}
	if got["bin/ro"] != 0o400 {
		t.Errorf("bin/ro mode = %o, want 0400", got["bin/ro"])
	}
}

func TestBackupDirs_RoundTripContents(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "libs", "foo.jar"), "JAR-BODY", 0o644)

	zipPath, err := BackupDirs(app, backup, []string{"libs"}, time.Now())
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	rc, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()

	var found bool
	for _, f := range rc.File {
		if f.Name != "libs/foo.jar" {
			continue
		}
		found = true
		body, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(body)
		body.Close()
		if string(data) != "JAR-BODY" {
			t.Errorf("body = %q", data)
		}
	}
	if !found {
		t.Error("libs/foo.jar not found in backup")
	}
}

func TestBackupDirs_MissingDirSkipped(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "x"), "y", 0o644)

	// "www" does not exist - should be skipped without error.
	zipPath, err := BackupDirs(app, backup, []string{"bin", "www"}, time.Now())
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip missing: %v", err)
	}
}

func TestBackupDirs_EmptyDirSetCreatesEmptyZip(t *testing.T) {
	tmp := t.TempDir()
	zipPath, err := BackupDirs(tmp, filepath.Join(tmp, "backup"), []string{"none"}, time.Now())
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip missing: %v", err)
	}
}

func TestBackupDirs_FilenameFormat(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "x"), "y", 0o644)

	ts := time.Date(2025, 1, 9, 8, 7, 6, 0, time.UTC)
	zipPath, err := BackupDirs(app, backup, []string{"bin"}, ts)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if filepath.Base(zipPath) != "2025-01-09-08-07-06.zip" {
		t.Errorf("filename = %q", filepath.Base(zipPath))
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
