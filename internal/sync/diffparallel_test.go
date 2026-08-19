package sync

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// buildTree writes files under root/dir and returns the directory name.
func buildTree(t *testing.T, root, dir string, files map[string][]byte) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func changeSet(d *DirDiff) map[string]string {
	out := make(map[string]string, len(d.Changes))
	for _, c := range d.Changes {
		out[c.Path] = c.Type.String()
	}
	return out
}

// The worker count must not influence the result: a single worker and a heavily
// oversubscribed pool have to produce the same classification.
func TestDiffDir_WorkerCountDoesNotChangeResult(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")

	refFiles := map[string][]byte{}
	liveFiles := map[string][]byte{}
	want := map[string]string{}
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("sub%d/file%d.bin", i%7, i)
		body := bytes.Repeat([]byte{byte(i)}, 4096+i)
		refFiles[name] = body
		switch i % 3 {
		case 0: // unchanged
			liveFiles[name] = body
		case 1: // modified, same length (forces a content comparison)
			alt := append([]byte(nil), body...)
			alt[len(alt)-1] ^= 0xFF
			liveFiles[name] = alt
			want[name] = "modified"
		case 2: // missing live-side
			want[name] = "added"
		}
	}
	liveFiles["sub0/only-live.bin"] = []byte("gone in the new release")
	want["sub0/only-live.bin"] = "removed"

	buildTree(t, ref, "bin", refFiles)
	buildTree(t, live, "bin", liveFiles)

	for _, workers := range []int{1, 2, 8, 64} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			d, err := DiffDir(ref, live, "bin", Options{Workers: workers})
			if err != nil {
				t.Fatalf("DiffDir: %v", err)
			}
			got := changeSet(d)
			if len(got) != len(want) {
				t.Fatalf("changes = %d, want %d", len(got), len(want))
			}
			for path, typ := range want {
				if got[path] != typ {
					t.Errorf("%s = %q, want %q", path, got[path], typ)
				}
			}
		})
	}
}

// The block comparison must catch a difference wherever it sits, including
// past the first block and in the very last byte.
func TestSameContent_DetectsDifferenceAtAnyOffset(t *testing.T) {
	tmp := t.TempDir()
	bufs := newCompareBuffers()

	size := 3*compareBlockSize + 17
	base := make([]byte, size)
	for i := range base {
		base[i] = byte(i)
	}

	offsets := []int{0, 1, compareBlockSize - 1, compareBlockSize, 2*compareBlockSize + 5, size - 1}
	for _, off := range offsets {
		t.Run(fmt.Sprintf("offset=%d", off), func(t *testing.T) {
			a := filepath.Join(tmp, fmt.Sprintf("a%d", off))
			b := filepath.Join(tmp, fmt.Sprintf("b%d", off))
			alt := append([]byte(nil), base...)
			alt[off] ^= 0xFF

			if err := os.WriteFile(a, base, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(b, alt, 0o644); err != nil {
				t.Fatal(err)
			}
			ia, _ := os.Stat(a)
			ib, _ := os.Stat(b)

			same, err := sameContent(a, ia, b, ib, bufs)
			if err != nil {
				t.Fatalf("sameContent: %v", err)
			}
			if same {
				t.Errorf("difference at offset %d not detected", off)
			}
		})
	}
}

func TestSameContent_IdenticalAndEmpty(t *testing.T) {
	tmp := t.TempDir()
	bufs := newCompareBuffers()

	body := bytes.Repeat([]byte("abc"), compareBlockSize) // spans several blocks
	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	if err := os.WriteFile(a, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ia, _ := os.Stat(a)
	ib, _ := os.Stat(b)
	same, err := sameContent(a, ia, b, ib, bufs)
	if err != nil {
		t.Fatalf("sameContent: %v", err)
	}
	if !same {
		t.Error("identical multi-block files reported as different")
	}

	// Two empty files short-circuit before any open.
	ea := filepath.Join(tmp, "ea")
	eb := filepath.Join(tmp, "eb")
	if err := os.WriteFile(ea, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eb, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	iea, _ := os.Stat(ea)
	ieb, _ := os.Stat(eb)
	same, err = sameContent(ea, iea, eb, ieb, bufs)
	if err != nil {
		t.Fatalf("sameContent (empty): %v", err)
	}
	if !same {
		t.Error("two empty files reported as different")
	}
}

// Progress must be reported for every reference file exactly once, with a
// monotonically rising done counter, even though workers run concurrently.
func TestDiffDir_ProgressCoversEveryFile(t *testing.T) {
	tmp := t.TempDir()
	ref := filepath.Join(tmp, "ref")
	live := filepath.Join(tmp, "live")

	files := map[string][]byte{}
	for i := 0; i < 25; i++ {
		files[fmt.Sprintf("f%02d", i)] = []byte(fmt.Sprintf("content %d", i))
	}
	buildTree(t, ref, "bin", files)
	buildTree(t, live, "bin", files)

	var (
		compared []int
		paths    []string
	)
	_, err := DiffDir(ref, live, "bin", Options{
		Workers: 8,
		Progress: func(dir string, done, total int, path string) {
			if total == 0 {
				return // listing phase
			}
			if total != len(files) {
				t.Errorf("total = %d, want %d", total, len(files))
			}
			compared = append(compared, done)
			paths = append(paths, path)
		},
	})
	if err != nil {
		t.Fatalf("DiffDir: %v", err)
	}

	if len(compared) != len(files) {
		t.Fatalf("progress calls = %d, want %d", len(compared), len(files))
	}
	sort.Ints(compared)
	for i, got := range compared {
		if got != i+1 {
			t.Fatalf("done counter %d = %d, want %d", i, got, i+1)
		}
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("path %q reported twice", p)
		}
		seen[p] = true
	}
}
