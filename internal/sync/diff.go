package sync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	gosync "sync"
	"sync/atomic"
)

// ChangeType classifies a single diff entry between the reference tree
// (extracted ZIP) and the live tree (app root).
type ChangeType int

const (
	Added ChangeType = iota
	Modified
	Removed
)

func (c ChangeType) String() string {
	switch c {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Removed:
		return "removed"
	}
	return "unknown"
}

// Change is one file-level difference.
type Change struct {
	Path string // path relative to the sync dir
	Type ChangeType
}

// DirDiff is the set of changes inside a single sync directory.
type DirDiff struct {
	Dir     string
	Changes []Change
}

// Counts returns added/modified/removed totals.
func (d *DirDiff) Counts() (add, mod, rem int) {
	for _, c := range d.Changes {
		switch c.Type {
		case Added:
			add++
		case Modified:
			mod++
		case Removed:
			rem++
		}
	}
	return
}

// ResolveRefRoot returns the directory inside extractedDir that should be
// treated as the reference root for diffing. It strips a single top-level
// wrapper folder (e.g. "app-1.2.3/") when the configured sync.directories
// are not present directly inside extractedDir but live one level deeper
// under exactly one sub-directory.
//
// Decision logic:
//  1. If any of syncDirs exists directly inside extractedDir, return
//     extractedDir unchanged.
//  2. Otherwise, list the top-level directories. If there is exactly one,
//     and at least one of the syncDirs exists inside it, return that wrapper.
//  3. Otherwise, return extractedDir unchanged (ambiguous, no strip).
//
// Files at the top level of extractedDir are ignored for the single-dir
// check — common Mac/Windows ZIPs include things like __MACOSX or README.txt
// alongside the wrapper folder.
func ResolveRefRoot(extractedDir string, syncDirs []string) string {
	for _, sd := range syncDirs {
		if stat, err := os.Stat(filepath.Join(extractedDir, sd)); err == nil && stat.IsDir() {
			return extractedDir
		}
	}

	entries, err := os.ReadDir(extractedDir)
	if err != nil {
		return extractedDir
	}

	var topDirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == "__MACOSX" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		topDirs = append(topDirs, e.Name())
	}
	if len(topDirs) != 1 {
		return extractedDir
	}

	candidate := filepath.Join(extractedDir, topDirs[0])
	for _, sd := range syncDirs {
		if stat, err := os.Stat(filepath.Join(candidate, sd)); err == nil && stat.IsDir() {
			return candidate
		}
	}
	return extractedDir
}

// Progress reports diff activity so a caller can render a status line.
//
// dir is the sync directory currently being processed and path the entry just
// handled. During the file-listing phase total is 0 and done counts the entries
// seen so far — the size of the tree is not known until the walk finishes.
// During the comparison phase total is the number of reference files in dir and
// done how many of them have been compared, so a real percentage is available
// for the expensive hashing part.
//
// The callback is invoked once per file and must be cheap; throttling is the
// caller's job.
// Calls may come from several goroutines; the diff serialises them, so the
// callback itself needs no locking of its own.
type Progress func(dir string, done, total int, path string)

// Options tunes a diff run. The zero value is valid: no progress reporting and
// an automatically chosen worker count.
type Options struct {
	// Progress, if non-nil, is called per file. See Progress.
	Progress Progress
	// Workers is the number of files compared concurrently. Zero or less
	// selects defaultWorkers().
	Workers int
}

// defaultWorkers picks the concurrency for content comparison. Comparing files
// is I/O-bound, so it pays to have several reads in flight, but past a handful
// of concurrent readers the gain flattens on SSDs and turns negative on
// spinning disks and network mounts — hence the cap.
func defaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Compute diffs every dir under both roots and returns one DirDiff per entry of dirs.
// Dirs that don't exist in the reference are still reported (existing live files become Removed).
// Dirs that don't exist in the live root are still reported (every ref file becomes Added).
func Compute(refRoot, liveRoot string, dirs []string, opt Options) ([]DirDiff, error) {
	out := make([]DirDiff, 0, len(dirs))
	for _, d := range dirs {
		dd, err := DiffDir(refRoot, liveRoot, d, opt)
		if err != nil {
			return nil, err
		}
		out = append(out, *dd)
	}
	return out, nil
}

// DiffDir compares the contents of refRoot/dir against liveRoot/dir.
// Returns a DirDiff even if one side is missing (treated as empty).
func DiffDir(refRoot, liveRoot, dir string, opt Options) (*DirDiff, error) {
	prog := opt.Progress

	// Both walks feed one counter so the listing phase shows the total work
	// discovered in this directory rather than restarting at zero halfway.
	var (
		mu   gosync.Mutex
		seen int
	)
	count := func(rel string) {
		mu.Lock()
		seen++
		n := seen
		if prog != nil {
			prog(dir, n, 0, rel)
		}
		mu.Unlock()
	}

	// The two trees live on independent paths, so walking them concurrently
	// overlaps their stat traffic. On a network-mounted install that roughly
	// halves the listing phase.
	var (
		wg              gosync.WaitGroup
		refFiles        map[string]fs.FileInfo
		liveFiles       map[string]fs.FileInfo
		refErr, liveErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		refFiles, refErr = walkRegular(filepath.Join(refRoot, dir), count)
	}()
	go func() {
		defer wg.Done()
		liveFiles, liveErr = walkRegular(filepath.Join(liveRoot, dir), count)
	}()
	wg.Wait()

	if refErr != nil {
		return nil, fmt.Errorf("diff: walk reference %s: %w", dir, refErr)
	}
	if liveErr != nil {
		return nil, fmt.Errorf("diff: walk live %s: %w", dir, liveErr)
	}

	changes, err := compareRefFiles(refRoot, liveRoot, dir, refFiles, liveFiles, opt)
	if err != nil {
		return nil, err
	}

	for rel := range liveFiles {
		if _, present := refFiles[rel]; !present {
			changes = append(changes, Change{Path: rel, Type: Removed})
		}
	}

	return &DirDiff{Dir: dir, Changes: changes}, nil
}

// compareRefFiles classifies every reference file as Added, Modified or
// unchanged. The work is spread over a pool of workers because the per-file
// cost is dominated by reading both copies off disk, which parallelises well;
// a single-threaded pass leaves most of the available I/O bandwidth unused on
// trees with thousands of files.
//
// Results are written into a slice indexed by the dispatch order, so the diff
// does not depend on which worker happens to finish first.
func compareRefFiles(refRoot, liveRoot, dir string, refFiles, liveFiles map[string]fs.FileInfo, opt Options) ([]Change, error) {
	total := len(refFiles)
	if total == 0 {
		return nil, nil
	}

	keys := make([]string, 0, total)
	for rel := range refFiles {
		keys = append(keys, rel)
	}

	found := make([]Change, total)
	keep := make([]bool, total)

	workers := opt.Workers
	if workers <= 0 {
		workers = defaultWorkers()
	}
	if workers > total {
		workers = total
	}

	var (
		wg       gosync.WaitGroup
		mu       gosync.Mutex // guards firstErr and the progress callback
		next     atomic.Int64
		done     atomic.Int64
		firstErr error
	)

	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	aborted := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return firstErr != nil
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One buffer pair per worker: reused for every file, so a large
			// tree does not churn the allocator.
			bufs := newCompareBuffers()
			for {
				i := int(next.Add(1)) - 1
				if i >= total || aborted() {
					return
				}
				rel := keys[i]

				liveInfo, present := liveFiles[rel]
				if !present {
					found[i], keep[i] = Change{Path: rel, Type: Added}, true
				} else {
					same, err := sameContent(
						filepath.Join(refRoot, dir, rel), refFiles[rel],
						filepath.Join(liveRoot, dir, rel), liveInfo,
						bufs,
					)
					if err != nil {
						fail(fmt.Errorf("diff: %s/%s: %w", dir, rel, err))
						return
					}
					if !same {
						found[i], keep[i] = Change{Path: rel, Type: Modified}, true
					}
				}

				if opt.Progress != nil {
					n := int(done.Add(1))
					mu.Lock()
					opt.Progress(dir, n, total, rel)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	var changes []Change
	for i, ok := range keep {
		if ok {
			changes = append(changes, found[i])
		}
	}
	return changes, nil
}

// walkRegular recursively lists regular files under root, calling onFile with
// each relative path as it is discovered.
// Symbolic links and other non-regular entries are skipped.
// If root does not exist, an empty map is returned (not an error).
func walkRegular(root string, onFile func(rel string)) (map[string]fs.FileInfo, error) {
	out := make(map[string]fs.FileInfo)

	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, devices
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		out[slash] = fi
		if onFile != nil {
			onFile(slash)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// compareBlockSize is the chunk both files are read in. Large enough that the
// per-read syscall overhead disappears, small enough that a pair of buffers per
// worker stays cheap.
const compareBlockSize = 256 << 10

// compareBuffers is the reusable read buffer pair of one comparison worker.
type compareBuffers struct {
	a, b []byte
}

func newCompareBuffers() *compareBuffers {
	return &compareBuffers{
		a: make([]byte, compareBlockSize),
		b: make([]byte, compareBlockSize),
	}
}

// sameContent reports whether two regular files have identical content.
//
// Strategy: size pre-check, then a lock-step block comparison that stops at the
// first difference. Hashing both files end to end (the earlier approach) always
// paid for every byte, even though a file that changed between two releases
// almost always differs early — and it read the same bytes anyway, so bailing
// out early is strictly cheaper.
func sameContent(pathA string, infoA fs.FileInfo, pathB string, infoB fs.FileInfo, bufs *compareBuffers) (bool, error) {
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	if infoA.Size() == 0 {
		return true, nil
	}

	fa, err := os.Open(pathA)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", pathA, err)
	}
	defer fa.Close()

	fb, err := os.Open(pathB)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", pathB, err)
	}
	defer fb.Close()

	for {
		na, errA := io.ReadFull(fa, bufs.a)
		nb, errB := io.ReadFull(fb, bufs.b)

		if na != nb || !bytes.Equal(bufs.a[:na], bufs.b[:nb]) {
			return false, nil
		}
		if err := readError(pathA, errA); err != nil {
			return false, err
		}
		if err := readError(pathB, errB); err != nil {
			return false, err
		}
		// Equal sizes mean both streams run out on the same iteration.
		if errA != nil || errB != nil {
			return true, nil
		}
	}
}

// readError filters the two ways io.ReadFull signals "the file ended here"
// (io.EOF on an empty read, io.ErrUnexpectedEOF on a partial one) out of the
// genuine I/O failures.
func readError(path string, err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return fmt.Errorf("read %s: %w", path, err)
}
