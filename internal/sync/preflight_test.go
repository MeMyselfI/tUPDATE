package sync

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// mkLiveFile is a tiny helper that creates a file with content under root.
func mkLiveFile(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// skipIfRoot bails out of permission-based assertions when the test runs as
// root, where the read-only/0555 bits are ignored and the probe would succeed.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission-bit probe semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
}

func TestPreflight_AllWritable(t *testing.T) {
	live := t.TempDir()
	mkLiveFile(t, live, "www/a.txt")   // Modified
	mkLiveFile(t, live, "www/old.txt") // Removed

	diffs := []DirDiff{{Dir: "www", Changes: []Change{
		{Path: "a.txt", Type: Modified},
		{Path: "old.txt", Type: Removed},
		{Path: "sub/new.txt", Type: Added}, // new file in a new subdir
	}}}

	if blocks := Preflight(live, diffs); len(blocks) != 0 {
		t.Fatalf("expected no blocks, got %v", blocks)
	}
}

func TestPreflight_MissingTargetsNotBlocked(t *testing.T) {
	live := t.TempDir()
	if err := os.MkdirAll(filepath.Join(live, "www"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Modified/Removed entries whose targets do not exist must not block:
	// Apply creates or skips them.
	diffs := []DirDiff{{Dir: "www", Changes: []Change{
		{Path: "gone.txt", Type: Modified},
		{Path: "already-gone.txt", Type: Removed},
	}}}

	if blocks := Preflight(live, diffs); len(blocks) != 0 {
		t.Fatalf("expected no blocks for missing targets, got %v", blocks)
	}
}

func TestPreflight_ReadOnlyFileBlocked(t *testing.T) {
	skipIfRoot(t)
	live := t.TempDir()
	p := mkLiveFile(t, live, "www/locked.txt")
	if err := os.Chmod(p, 0o400); err != nil { // read-only → O_WRONLY denied
		t.Fatal(err)
	}

	diffs := []DirDiff{{Dir: "www", Changes: []Change{
		{Path: "locked.txt", Type: Modified},
	}}}

	blocks := Preflight(live, diffs)
	if len(blocks) != 1 || blocks[0].Path != "www/locked.txt" {
		t.Fatalf("expected one block for www/locked.txt, got %v", blocks)
	}
}

func TestPreflight_AddedIntoReadonlyDirBlocked(t *testing.T) {
	skipIfRoot(t)
	live := t.TempDir()
	dir := filepath.Join(live, "www")
	if err := os.MkdirAll(dir, 0o555); err != nil { // no write → cannot create
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // let TempDir cleanup remove it

	diffs := []DirDiff{{Dir: "www", Changes: []Change{
		{Path: "new.txt", Type: Added},
	}}}

	blocks := Preflight(live, diffs)
	if len(blocks) != 1 || blocks[0].Path != "www/new.txt" {
		t.Fatalf("expected one block for www/new.txt, got %v", blocks)
	}
}
