package sync

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApply_AddedFileCreated(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(ref, "bin", "new.sh"), "#!/bin/sh\nnew\n")

	diff := []DirDiff{{Dir: "bin", Changes: []Change{{Path: "new.sh", Type: Added}}}}
	if err := Apply(ref, live, diff, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(live, "bin", "new.sh"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "#!/bin/sh\nnew\n" {
		t.Errorf("contents = %q", got)
	}
}

func TestApply_ModifiedFileOverwritten(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(ref, "etc", "config.txt"), "NEW")
	writeFile(t, filepath.Join(live, "etc", "config.txt"), "OLD")

	diff := []DirDiff{{Dir: "etc", Changes: []Change{{Path: "config.txt", Type: Modified}}}}
	if err := Apply(ref, live, diff, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(live, "etc", "config.txt"))
	if string(got) != "NEW" {
		t.Errorf("contents = %q, want NEW", got)
	}
}

func TestApply_RemovedFileDeleted(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(live, "etc", "stale.cfg"), "x")

	diff := []DirDiff{{Dir: "etc", Changes: []Change{{Path: "stale.cfg", Type: Removed}}}}
	if err := Apply(ref, live, diff, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(live, "etc", "stale.cfg")); !os.IsNotExist(err) {
		t.Errorf("file should have been removed, stat err=%v", err)
	}
}

func TestApply_PreservesModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits irrelevant on Windows")
	}
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	srcPath := filepath.Join(ref, "bin", "exec.sh")
	writeFile(t, srcPath, "#!/bin/sh\nx\n")
	if err := os.Chmod(srcPath, 0o755); err != nil {
		t.Fatal(err)
	}

	diff := []DirDiff{{Dir: "bin", Changes: []Change{{Path: "exec.sh", Type: Added}}}}
	if err := Apply(ref, live, diff, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	info, err := os.Stat(filepath.Join(live, "bin", "exec.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("perm = %o, want 0755", perm)
	}
}

func TestApply_RemovesEmptyParentDirs(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(live, "etc", "a", "b", "lonely.txt"), "x")

	diff := []DirDiff{{Dir: "etc", Changes: []Change{{Path: "a/b/lonely.txt", Type: Removed}}}}
	if err := Apply(filepath.Join(tmp, "ref"), live, diff, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, p := range []string{"etc/a/b", "etc/a"} {
		if _, err := os.Stat(filepath.Join(live, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("dir %s should be pruned, err=%v", p, err)
		}
	}
	// 'etc' should still exist (it's the dir root).
	if _, err := os.Stat(filepath.Join(live, "etc")); err != nil {
		t.Errorf("etc/ should still exist, err=%v", err)
	}
}

func TestApply_RemovingMissingFileIsNoOp(t *testing.T) {
	tmp := t.TempDir()
	diff := []DirDiff{{Dir: "etc", Changes: []Change{{Path: "ghost.txt", Type: Removed}}}}
	if err := Apply(filepath.Join(tmp, "ref"), filepath.Join(tmp, "live"), diff, nil); err != nil {
		t.Errorf("Apply should ignore missing file: %v", err)
	}
}

func TestApply_AppliesAcrossMultipleDirs(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(ref, "bin", "a.sh"), "1")
	writeFile(t, filepath.Join(ref, "www", "i.html"), "v2")
	writeFile(t, filepath.Join(live, "www", "i.html"), "v1")
	writeFile(t, filepath.Join(live, "libs", "old.jar"), "x")

	diffs := []DirDiff{
		{Dir: "bin", Changes: []Change{{Path: "a.sh", Type: Added}}},
		{Dir: "www", Changes: []Change{{Path: "i.html", Type: Modified}}},
		{Dir: "libs", Changes: []Change{{Path: "old.jar", Type: Removed}}},
	}
	if err := Apply(ref, live, diffs, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cases := map[string]string{
		"bin/a.sh":   "1",
		"www/i.html": "v2",
	}
	for rel, want := range cases {
		got, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(live, "libs", "old.jar")); !os.IsNotExist(err) {
		t.Errorf("libs/old.jar should be removed")
	}
}

func TestApply_OnFileCallback(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")
	writeFile(t, filepath.Join(ref, "bin", "a.sh"), "a")
	writeFile(t, filepath.Join(live, "bin", "old.sh"), "old")

	diff := []DirDiff{{Dir: "bin", Changes: []Change{
		{Path: "a.sh", Type: Added},
		{Path: "old.sh", Type: Removed},
	}}}
	var seen []string
	if err := Apply(ref, live, diff, func(n string) { seen = append(seen, n) }); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(seen) != 2 || seen[0] != "bin/a.sh" || seen[1] != "bin/old.sh" {
		t.Errorf("onFile names = %v, want [bin/a.sh bin/old.sh]", seen)
	}
}
