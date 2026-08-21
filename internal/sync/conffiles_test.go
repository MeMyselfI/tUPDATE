package sync

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeConfFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

func confRoots(t *testing.T) (ref, live string) {
	t.Helper()
	tmp := t.TempDir()
	ref = filepath.Join(tmp, "ref")
	live = filepath.Join(tmp, "live")
	for _, d := range []string{ref, live} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return ref, live
}

func stateOf(t *testing.T, files []ConfFile, path string) ConfState {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f.State
		}
	}
	t.Fatalf("path %q not in result %v", path, files)
	return ConfSame
}

func TestCompareConfFiles_States(t *testing.T) {
	ref, live := confRoots(t)

	writeConfFile(t, ref, "conf/same.properties", "a=1\n")
	writeConfFile(t, live, "conf/same.properties", "a=1\n")

	writeConfFile(t, ref, "conf/changed.properties", "a=2\n")
	writeConfFile(t, live, "conf/changed.properties", "a=1\n")

	writeConfFile(t, ref, "conf/new.properties", "a=3\n")

	writeConfFile(t, live, "conf/gone.properties", "a=4\n")

	list := []string{
		"conf/same.properties",
		"conf/changed.properties",
		"conf/new.properties",
		"conf/gone.properties",
	}
	got, err := CompareConfFiles(ref, live, list)
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}
	if len(got) != len(list) {
		t.Fatalf("len = %d, want %d", len(got), len(list))
	}
	if s := stateOf(t, got, "conf/same.properties"); s != ConfSame {
		t.Errorf("same: state = %d, want ConfSame", s)
	}
	if s := stateOf(t, got, "conf/changed.properties"); s != ConfModified {
		t.Errorf("changed: state = %d, want ConfModified", s)
	}
	if s := stateOf(t, got, "conf/new.properties"); s != ConfMissingLive {
		t.Errorf("new: state = %d, want ConfMissingLive", s)
	}
	if s := stateOf(t, got, "conf/gone.properties"); s != ConfMissingRef {
		t.Errorf("gone: state = %d, want ConfMissingRef", s)
	}
	if n := ConfUpdateCount(got); n != 2 {
		t.Errorf("ConfUpdateCount = %d, want 2", n)
	}
}

func TestCompareConfFiles_SameSizeDifferentContent(t *testing.T) {
	ref, live := confRoots(t)
	writeConfFile(t, ref, "conf/a.properties", "value=aaaa\n")
	writeConfFile(t, live, "conf/a.properties", "value=bbbb\n")

	got, err := CompareConfFiles(ref, live, []string{"conf/a.properties"})
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}
	if got[0].State != ConfModified {
		t.Errorf("state = %d, want ConfModified", got[0].State)
	}
}

func TestCompareConfFiles_EmptyListIsNoop(t *testing.T) {
	ref, live := confRoots(t)
	got, err := CompareConfFiles(ref, live, nil)
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestApplyConfFiles_WritesOnlyDivergingEntries(t *testing.T) {
	ref, live := confRoots(t)

	writeConfFile(t, ref, "conf/same.properties", "a=1\n")
	writeConfFile(t, live, "conf/same.properties", "a=1\n")
	writeConfFile(t, ref, "conf/changed.properties", "a=new\n")
	writeConfFile(t, live, "conf/changed.properties", "a=old\n")
	writeConfFile(t, ref, "conf/new.properties", "a=fresh\n")
	writeConfFile(t, live, "conf/gone.properties", "a=keepme\n")

	list := []string{
		"conf/same.properties",
		"conf/changed.properties",
		"conf/new.properties",
		"conf/gone.properties",
	}
	files, err := CompareConfFiles(ref, live, list)
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}

	var touched []string
	if err := ApplyConfFiles(ref, live, files, func(name string) { touched = append(touched, name) }); err != nil {
		t.Fatalf("ApplyConfFiles: %v", err)
	}
	if len(touched) != 2 {
		t.Errorf("touched = %v, want 2 entries", touched)
	}

	assertContent := func(rel, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", rel, b, want)
		}
	}
	assertContent("conf/changed.properties", "a=new\n")
	assertContent("conf/new.properties", "a=fresh\n")
	// A file the release does not ship must survive untouched — a stale
	// conf.files entry must never delete or blank a live config.
	assertContent("conf/gone.properties", "a=keepme\n")
}

func TestPreflightConfFiles_ReadOnlyTargetBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based read-only probe is unreliable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only bit")
	}
	ref, live := confRoots(t)
	writeConfFile(t, ref, "conf/a.properties", "a=new\n")
	target := writeConfFile(t, live, "conf/a.properties", "a=old\n")
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })

	files, err := CompareConfFiles(ref, live, []string{"conf/a.properties"})
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}
	blocks := PreflightConfFiles(live, files)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %v, want 1", blocks)
	}
	if blocks[0].Path != "conf/a.properties" {
		t.Errorf("blocked path = %q", blocks[0].Path)
	}
}

func TestPreflightConfFiles_IdenticalFilesNotProbed(t *testing.T) {
	ref, live := confRoots(t)
	writeConfFile(t, ref, "conf/a.properties", "a=1\n")
	writeConfFile(t, live, "conf/a.properties", "a=1\n")

	files, err := CompareConfFiles(ref, live, []string{"conf/a.properties"})
	if err != nil {
		t.Fatalf("CompareConfFiles: %v", err)
	}
	if blocks := PreflightConfFiles(live, files); len(blocks) != 0 {
		t.Errorf("blocks = %v, want none", blocks)
	}
}
