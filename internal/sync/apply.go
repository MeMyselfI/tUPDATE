package sync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Apply mutates liveRoot so its sync dirs match refRoot per the precomputed diffs.
//
// Added/Modified entries: copied from refRoot to liveRoot with mode bits preserved.
// Removed entries: deleted from liveRoot. Empty parent directories are pruned afterwards.
//
// On the first error the function returns; partial application is possible — callers
// should report the backup path so the user can roll back manually.
func Apply(refRoot, liveRoot string, diffs []DirDiff) error {
	for _, d := range diffs {
		for _, c := range d.Changes {
			switch c.Type {
			case Added, Modified:
				if err := copyFile(
					filepath.Join(refRoot, d.Dir, filepath.FromSlash(c.Path)),
					filepath.Join(liveRoot, d.Dir, filepath.FromSlash(c.Path)),
				); err != nil {
					return fmt.Errorf("apply: %s/%s: %w", d.Dir, c.Path, err)
				}
			case Removed:
				target := filepath.Join(liveRoot, d.Dir, filepath.FromSlash(c.Path))
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("apply: remove %s/%s: %w", d.Dir, c.Path, err)
				}
				pruneEmptyDirs(filepath.Dir(target), filepath.Join(liveRoot, d.Dir))
			}
		}
	}
	return nil
}

// copyFile copies src to dst, creating parent directories and preserving the source's mode bits.
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", src, err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir parent of %s: %w", dst, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open dest %s: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	if err := os.Chmod(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}
	return nil
}

// pruneEmptyDirs removes empty directories upward from `dir` toward `stopAt` (exclusive).
// It silently stops at the first non-empty directory or any error.
// stopAt is never removed even if it ends up empty.
func pruneEmptyDirs(dir, stopAt string) {
	stopAtAbs, _ := filepath.Abs(stopAt)
	for {
		abs, err := filepath.Abs(dir)
		if err != nil || abs == stopAtAbs {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		if len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
