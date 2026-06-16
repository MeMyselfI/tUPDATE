package sync

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cespare/xxhash/v2"
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

// Compute diffs every dir under both roots and returns one DirDiff per entry of dirs.
// Dirs that don't exist in the reference are still reported (existing live files become Removed).
// Dirs that don't exist in the live root are still reported (every ref file becomes Added).
func Compute(refRoot, liveRoot string, dirs []string) ([]DirDiff, error) {
	out := make([]DirDiff, 0, len(dirs))
	for _, d := range dirs {
		dd, err := DiffDir(refRoot, liveRoot, d)
		if err != nil {
			return nil, err
		}
		out = append(out, *dd)
	}
	return out, nil
}

// DiffDir compares the contents of refRoot/dir against liveRoot/dir.
// Returns a DirDiff even if one side is missing (treated as empty).
func DiffDir(refRoot, liveRoot, dir string) (*DirDiff, error) {
	refFiles, err := walkRegular(filepath.Join(refRoot, dir))
	if err != nil {
		return nil, fmt.Errorf("diff: walk reference %s: %w", dir, err)
	}
	liveFiles, err := walkRegular(filepath.Join(liveRoot, dir))
	if err != nil {
		return nil, fmt.Errorf("diff: walk live %s: %w", dir, err)
	}

	var changes []Change

	for rel, refInfo := range refFiles {
		liveInfo, present := liveFiles[rel]
		if !present {
			changes = append(changes, Change{Path: rel, Type: Added})
			continue
		}
		same, err := sameContent(
			filepath.Join(refRoot, dir, rel), refInfo,
			filepath.Join(liveRoot, dir, rel), liveInfo,
		)
		if err != nil {
			return nil, fmt.Errorf("diff: %s/%s: %w", dir, rel, err)
		}
		if !same {
			changes = append(changes, Change{Path: rel, Type: Modified})
		}
	}

	for rel := range liveFiles {
		if _, present := refFiles[rel]; !present {
			changes = append(changes, Change{Path: rel, Type: Removed})
		}
	}

	return &DirDiff{Dir: dir, Changes: changes}, nil
}

// walkRegular recursively lists regular files under root.
// Symbolic links and other non-regular entries are skipped.
// If root does not exist, an empty map is returned (not an error).
func walkRegular(root string) (map[string]fs.FileInfo, error) {
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
		out[filepath.ToSlash(rel)] = fi
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// sameContent reports whether two regular files have identical content.
// Strategy: size pre-check, then xxhash64 of both files.
func sameContent(pathA string, infoA fs.FileInfo, pathB string, infoB fs.FileInfo) (bool, error) {
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	hashA, err := hashFile(pathA)
	if err != nil {
		return false, err
	}
	hashB, err := hashFile(pathB)
	if err != nil {
		return false, err
	}
	return hashA == hashB, nil
}

func hashFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("hashFile open %s: %w", path, err)
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, fmt.Errorf("hashFile copy %s: %w", path, err)
	}
	return h.Sum64(), nil
}
