package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type zipEntry struct {
	name string
	body string
	mode os.FileMode
	dir  bool
}

func buildZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()

	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.dir {
			header.Name = e.name
			if header.Name[len(header.Name)-1] != '/' {
				header.Name += "/"
			}
			header.SetMode(e.mode | os.ModeDir)
		} else {
			header.SetMode(e.mode)
		}
		wr, err := w.CreateHeader(header)
		if err != nil {
			t.Fatalf("create header %s: %v", e.name, err)
		}
		if !e.dir {
			if _, err := wr.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
		}
	}
}

func TestExtract_Success(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "src.zip")
	buildZip(t, zipPath, []zipEntry{
		{name: "bin/run.sh", body: "#!/bin/sh\necho hi\n", mode: 0o755},
		{name: "etc/config.txt", body: "x=1\n", mode: 0o644},
		{name: "www/index.html", body: "<html></html>", mode: 0o644},
		{name: "libs/sub/inner.txt", body: "nested", mode: 0o600},
	})

	out := filepath.Join(tmp, "out")
	if err := Extract(zipPath, out); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cases := map[string]string{
		"bin/run.sh":         "#!/bin/sh\necho hi\n",
		"etc/config.txt":     "x=1\n",
		"www/index.html":     "<html></html>",
		"libs/sub/inner.txt": "nested",
	}
	for rel, want := range cases {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: contents = %q, want %q", rel, got, want)
		}
	}
}

func TestExtract_PreservesUnixModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not meaningful on Windows")
	}
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "src.zip")
	buildZip(t, zipPath, []zipEntry{
		{name: "bin/exec", body: "x", mode: 0o755},
		{name: "etc/readonly", body: "y", mode: 0o400},
	})

	out := filepath.Join(tmp, "out")
	if err := Extract(zipPath, out); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	info, err := os.Stat(filepath.Join(out, "bin", "exec"))
	if err != nil {
		t.Fatalf("stat exec: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("bin/exec perm = %o, want 0755", perm)
	}

	info2, err := os.Stat(filepath.Join(out, "etc", "readonly"))
	if err != nil {
		t.Fatalf("stat readonly: %v", err)
	}
	if perm := info2.Mode().Perm(); perm != 0o400 {
		t.Errorf("etc/readonly perm = %o, want 0400", perm)
	}
}

func TestExtract_ZipSlipRejected(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	buildZip(t, zipPath, []zipEntry{
		{name: "../escape.txt", body: "pwned", mode: 0o644},
	})

	out := filepath.Join(tmp, "out")
	err := Extract(zipPath, out)
	if err == nil {
		t.Fatal("expected zip-slip error")
	}

	if _, statErr := os.Stat(filepath.Join(tmp, "escape.txt")); statErr == nil {
		t.Error("zip-slip created file outside destination")
	}
}

func TestExtract_NestedDirsCreated(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "src.zip")
	buildZip(t, zipPath, []zipEntry{
		{name: "a/b/c/d.txt", body: "deep", mode: 0o644},
	})
	out := filepath.Join(tmp, "out")
	if err := Extract(zipPath, out); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "a", "b", "c", "d.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "deep" {
		t.Errorf("contents = %q", got)
	}
}

func TestExtract_NonExistentZip(t *testing.T) {
	err := Extract(filepath.Join(t.TempDir(), "missing.zip"), t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing zip")
	}
}
