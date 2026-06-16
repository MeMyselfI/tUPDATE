package sync

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func changeMap(d *DirDiff) map[string]ChangeType {
	m := make(map[string]ChangeType, len(d.Changes))
	for _, c := range d.Changes {
		m[c.Path] = c.Type
	}
	return m
}

func TestDiffDir_AllUnchanged(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(ref, "bin", "a.txt"), "alpha")
	writeFile(t, filepath.Join(live, "bin", "a.txt"), "alpha")

	d, err := DiffDir(ref, live, "bin")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	if len(d.Changes) != 0 {
		t.Errorf("expected 0 changes, got %v", d.Changes)
	}
}

func TestDiffDir_DetectsAddedModifiedRemoved(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")

	// Same in both
	writeFile(t, filepath.Join(ref, "bin", "same.txt"), "same-content")
	writeFile(t, filepath.Join(live, "bin", "same.txt"), "same-content")

	// Modified (different content, same size)
	writeFile(t, filepath.Join(ref, "bin", "mod-same-size.txt"), "AAAA")
	writeFile(t, filepath.Join(live, "bin", "mod-same-size.txt"), "BBBB")

	// Modified (different size)
	writeFile(t, filepath.Join(ref, "bin", "mod-diff-size.txt"), "shortA")
	writeFile(t, filepath.Join(live, "bin", "mod-diff-size.txt"), "muchlongerB")

	// Added (ref only)
	writeFile(t, filepath.Join(ref, "bin", "added.txt"), "new-file")

	// Removed (live only)
	writeFile(t, filepath.Join(live, "bin", "removed.txt"), "old-file")

	d, err := DiffDir(ref, live, "bin")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}

	got := changeMap(d)
	want := map[string]ChangeType{
		"mod-same-size.txt": Modified,
		"mod-diff-size.txt": Modified,
		"added.txt":         Added,
		"removed.txt":       Removed,
	}
	if len(got) != len(want) {
		t.Errorf("change count = %d, want %d, changes=%v", len(got), len(want), d.Changes)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
}

func TestDiffDir_Recursive(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(ref, "www", "sub", "inner.html"), "<new/>")
	writeFile(t, filepath.Join(live, "www", "sub", "inner.html"), "<old/>")

	d, err := DiffDir(ref, live, "www")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	if len(d.Changes) != 1 || d.Changes[0].Type != Modified {
		t.Errorf("expected 1 modified, got %v", d.Changes)
	}
	if d.Changes[0].Path != "sub/inner.html" {
		t.Errorf("relative path = %q", d.Changes[0].Path)
	}
}

func TestDiffDir_RefDirMissing(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(live, "etc", "a.cfg"), "x")

	d, err := DiffDir(filepath.Join(tmp, "ref-missing"), live, "etc")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	if len(d.Changes) != 1 || d.Changes[0].Type != Removed {
		t.Errorf("expected 1 removed, got %v", d.Changes)
	}
}

func TestDiffDir_LiveDirMissing(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	writeFile(t, filepath.Join(ref, "libs", "x.jar"), "jar")

	d, err := DiffDir(ref, filepath.Join(tmp, "live-missing"), "libs")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	if len(d.Changes) != 1 || d.Changes[0].Type != Added {
		t.Errorf("expected 1 added, got %v", d.Changes)
	}
}

func TestDiffDir_BothMissing(t *testing.T) {
	tmp := t.TempDir()
	d, err := DiffDir(filepath.Join(tmp, "no-ref"), filepath.Join(tmp, "no-live"), "etc")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	if len(d.Changes) != 0 {
		t.Errorf("expected 0 changes, got %v", d.Changes)
	}
}

func TestCompute_MultipleDirs(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(ref, "bin", "a.sh"), "1")
	writeFile(t, filepath.Join(live, "bin", "a.sh"), "1") // unchanged
	writeFile(t, filepath.Join(ref, "www", "i.html"), "v2")
	writeFile(t, filepath.Join(live, "www", "i.html"), "v1") // modified
	writeFile(t, filepath.Join(ref, "etc", "new.cfg"), "x")  // added

	diffs, err := Compute(ref, live, []string{"bin", "www", "etc", "libs"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(diffs) != 4 {
		t.Fatalf("len(diffs) = %d, want 4", len(diffs))
	}
	byDir := make(map[string]*DirDiff)
	for i := range diffs {
		byDir[diffs[i].Dir] = &diffs[i]
	}
	if a, m, r := byDir["bin"].Counts(); a+m+r != 0 {
		t.Errorf("bin should have 0 changes, got +%d ~%d -%d", a, m, r)
	}
	if a, m, r := byDir["www"].Counts(); a != 0 || m != 1 || r != 0 {
		t.Errorf("www counts = +%d ~%d -%d, want 0/1/0", a, m, r)
	}
	if a, m, r := byDir["etc"].Counts(); a != 1 || m != 0 || r != 0 {
		t.Errorf("etc counts = +%d ~%d -%d, want 1/0/0", a, m, r)
	}
	if a, m, r := byDir["libs"].Counts(); a+m+r != 0 {
		t.Errorf("libs should have 0 changes, got +%d ~%d -%d", a, m, r)
	}
}

func TestResolveRefRoot_DirectLayoutUnchanged(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveRefRoot(tmp, []string{"bin", "www"})
	if got != tmp {
		t.Errorf("expected %q, got %q", tmp, got)
	}
}

func TestResolveRefRoot_StripsSingleWrapperFolder(t *testing.T) {
	tmp := t.TempDir()
	wrapper := filepath.Join(tmp, "tOSCE-Server")
	if err := os.MkdirAll(filepath.Join(wrapper, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wrapper, "www"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := ResolveRefRoot(tmp, []string{"bin", "www", "etc", "libs"})
	if got != wrapper {
		t.Errorf("expected %q, got %q", wrapper, got)
	}
}

func TestResolveRefRoot_IgnoresMACOSXSibling(t *testing.T) {
	tmp := t.TempDir()
	wrapper := filepath.Join(tmp, "tOSCE-Server")
	if err := os.MkdirAll(filepath.Join(wrapper, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "__MACOSX"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveRefRoot(tmp, []string{"bin"})
	if got != wrapper {
		t.Errorf("expected wrapper %q, got %q", wrapper, got)
	}
}

func TestResolveRefRoot_MultipleTopDirsNoStrip(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"folderA", "folderB"} {
		if err := os.MkdirAll(filepath.Join(tmp, d, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := ResolveRefRoot(tmp, []string{"bin"})
	if got != tmp {
		t.Errorf("ambiguous wrapper case should not strip, got %q", got)
	}
}

func TestResolveRefRoot_WrapperWithoutSyncDirsNoStrip(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "unrelated", "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveRefRoot(tmp, []string{"bin"})
	if got != tmp {
		t.Errorf("wrapper without sync dirs should not strip, got %q", got)
	}
}

func TestResolveRefRoot_TopLevelFilesIgnored(t *testing.T) {
	tmp := t.TempDir()
	wrapper := filepath.Join(tmp, "wrapper")
	if err := os.MkdirAll(filepath.Join(wrapper, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "README.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveRefRoot(tmp, []string{"bin"})
	if got != wrapper {
		t.Errorf("README at top should be ignored, got %q", got)
	}
}

func TestResolveRefRoot_SyncDirAtTopWinsOverWrapper(t *testing.T) {
	// Pathological: a dir named "bin" at top and another wrapper alongside.
	// We must NOT pick the wrapper if bin/ already exists at the top level.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "bin", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "extra"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveRefRoot(tmp, []string{"bin"})
	if got != tmp {
		t.Errorf("top-level bin/ should win, got %q", got)
	}
}

func TestDiffDir_Symlinks(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live", "bin")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(live, "real.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(live, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	d, err := DiffDir(filepath.Join(tmp, "ref-missing"), filepath.Join(tmp, "live"), "bin")
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}
	// Only real.txt should be flagged (symlinks skipped).
	var names []string
	for _, c := range d.Changes {
		names = append(names, c.Path)
	}
	sort.Strings(names)
	if len(names) != 1 || names[0] != "real.txt" {
		t.Errorf("expected only real.txt to be Removed, got %v", names)
	}
}
