package archive

import (
	"archive/tar"
	"archive/zip"
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
	outPath, err := BackupDirs(app, backup, []string{"bin", "etc"}, ts, BackupOptions{}, nil)
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

	outPath, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), BackupOptions{}, nil)
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

	outPath, err := BackupDirs(app, backup, []string{"libs"}, time.Now(), BackupOptions{}, nil)
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
	_, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), BackupOptions{}, func(done, total int64) {
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

func TestBackupDirs_SkipsBackupDirInsideSyncDir(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	// backupDir lives *inside* the synced "data" dir — the self-inclusion trap.
	backup := filepath.Join(app, "data", "backup")

	writeFileMode(t, filepath.Join(app, "data", "real.txt"), "keep", 0o644)
	writeFileMode(t, filepath.Join(backup, "old-2020.tar.xz"), "stale-backup", 0o644)

	outPath, err := BackupDirs(app, backup, []string{"data"}, time.Now(), BackupOptions{}, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	entries := readBackup(t, outPath)
	if _, ok := entries["data/real.txt"]; !ok {
		t.Error("data/real.txt should be in the backup")
	}
	for name := range entries {
		if strings.HasPrefix(name, "data/backup/") {
			t.Errorf("backup dir must be excluded, but archive contains %q", name)
		}
	}
}

func TestBackupDirs_MissingDirSkipped(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "x"), "y", 0o644)

	// "www" does not exist - should be skipped without error.
	outPath, err := BackupDirs(app, backup, []string{"bin", "www"}, time.Now(), BackupOptions{}, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
}

func TestBackupDirs_EmptyDirSetCreatesEmptyArchive(t *testing.T) {
	tmp := t.TempDir()
	outPath, err := BackupDirs(tmp, filepath.Join(tmp, "backup"), []string{"none"}, time.Now(), BackupOptions{}, nil)
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
	outPath, err := BackupDirs(app, backup, []string{"bin"}, ts, BackupOptions{}, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if filepath.Base(outPath) != "2025-01-09-08-07-06.tar.xz" {
		t.Errorf("filename = %q", filepath.Base(outPath))
	}
}

func TestBackupDirs_ZipFormat(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")
	writeFileMode(t, filepath.Join(app, "bin", "run.sh"), "hi", 0o644)

	ts := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	outPath, err := BackupDirs(app, backup, []string{"bin"}, ts, BackupOptions{Format: FormatZip, Level: LevelMax}, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if filepath.Base(outPath) != "2025-01-02-03-04-05.zip" {
		t.Errorf("name = %q, want 2025-01-02-03-04-05.zip", filepath.Base(outPath))
	}

	rc, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer rc.Close()
	var found bool
	for _, f := range rc.File {
		if f.Name == "bin/run.sh" {
			found = true
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			if string(b) != "hi" {
				t.Errorf("body = %q", b)
			}
		}
	}
	if !found {
		t.Error("bin/run.sh missing in zip")
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

func TestBackupDirs_IncludeDirectoryAndFile(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")

	writeFileMode(t, filepath.Join(app, "bin", "run.sh"), "run\n", 0o755)
	writeFileMode(t, filepath.Join(app, "conf", "server.properties"), "port=443\n", 0o600)
	writeFileMode(t, filepath.Join(app, "conf", "sub", "extra.properties"), "x=1\n", 0o644)
	writeFileMode(t, filepath.Join(app, "notes.txt"), "hello\n", 0o644)

	opts := BackupOptions{Include: []string{"conf", "notes.txt", "does-not-exist"}}
	outPath, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), opts, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	entries := readBackup(t, outPath)
	var names []string
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"bin/run.sh", "conf/server.properties", "conf/sub/extra.properties", "notes.txt"}
	if !equalStringSlice(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	if got := entries["conf/server.properties"].body; got != "port=443\n" {
		t.Errorf("conf body = %q", got)
	}
}

func TestBackupDirs_IncludeOverlappingSyncDirIsNotDuplicated(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")

	writeFileMode(t, filepath.Join(app, "etc", "config.txt"), "k=v\n", 0o644)

	// "etc/config.txt" is already covered by the "etc" sync dir. Writing the
	// same name twice would produce an archive with duplicate members.
	opts := BackupOptions{Include: []string{"etc", "etc/config.txt"}}
	outPath, err := BackupDirs(app, backup, []string{"etc"}, time.Now(), opts, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	xr, err := xz.NewReader(f)
	if err != nil {
		t.Fatalf("xz: %v", err)
	}
	tr := tar.NewReader(xr)
	counts := make(map[string]int)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		counts[hdr.Name]++
	}
	if counts["etc/config.txt"] != 1 {
		t.Errorf("etc/config.txt appears %d times, want 1", counts["etc/config.txt"])
	}
}

func TestBackupDirs_IncludeCountsTowardsProgressTotal(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(tmp, "backup")

	writeFileMode(t, filepath.Join(app, "bin", "a.bin"), strings.Repeat("a", 100), 0o644)
	writeFileMode(t, filepath.Join(app, "conf", "b.properties"), strings.Repeat("b", 50), 0o644)

	var lastTotal, lastDone int64
	opts := BackupOptions{Include: []string{"conf"}}
	if _, err := BackupDirs(app, backup, []string{"bin"}, time.Now(), opts, func(done, total int64) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	if lastTotal != 150 {
		t.Errorf("total = %d, want 150", lastTotal)
	}
	if lastDone != 150 {
		t.Errorf("done = %d, want 150", lastDone)
	}
}

func TestBackupDirs_IncludeDoesNotArchiveBackupDir(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	backup := filepath.Join(app, "conf", "backup")

	writeFileMode(t, filepath.Join(app, "conf", "server.properties"), "k=v\n", 0o644)

	// backup.directory nested inside an include path must be skipped, or the
	// walk would archive the archive it is currently writing.
	opts := BackupOptions{Include: []string{"conf"}}
	outPath, err := BackupDirs(app, backup, []string{}, time.Now(), opts, nil)
	if err != nil {
		t.Fatalf("BackupDirs: %v", err)
	}
	for name := range readBackup(t, outPath) {
		if strings.HasPrefix(name, "conf/backup/") {
			t.Errorf("archive contains its own backup dir entry %q", name)
		}
	}
}
