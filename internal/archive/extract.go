package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extract unpacks zipPath into destDir.
// Directories and file mode bits are preserved (best-effort on Windows).
// If progress is non-nil it is called as entries are written, with done as the
// number of uncompressed bytes extracted so far and total the sum of all entry
// sizes. Returns an error on zip-slip path traversal, unreadable entries, or
// write failures.
func Extract(zipPath, destDir string, progress Progress) error {
	rc, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", zipPath, err)
	}
	defer rc.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("archive: mkdir %s: %w", destDir, err)
	}

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("archive: abs %s: %w", destDir, err)
	}

	var total int64
	for _, f := range rc.File {
		if !f.FileInfo().IsDir() {
			total += int64(f.UncompressedSize64)
		}
	}

	if progress != nil {
		progress(0, total)
	}
	var done int64
	for _, f := range rc.File {
		if err := extractEntry(f, absDest, &done, total, progress); err != nil {
			return err
		}
	}
	return nil
}

func extractEntry(f *zip.File, absDest string, done *int64, total int64, progress Progress) error {
	cleanName := filepath.FromSlash(f.Name)
	target := filepath.Join(absDest, cleanName)

	rel, err := filepath.Rel(absDest, target)
	if err != nil {
		return fmt.Errorf("archive: invalid path %q: %w", f.Name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive: zip-slip detected, entry %q escapes destination", f.Name)
	}

	mode := f.Mode()
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, dirMode(mode))
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("archive: mkdir parent of %s: %w", target, err)
	}

	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("archive: open entry %s: %w", f.Name, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode(mode))
	if err != nil {
		return fmt.Errorf("archive: create %s: %w", target, err)
	}

	cw := &countingWriter{w: dst, done: done, total: total, progress: progress}
	if _, err := io.Copy(cw, src); err != nil {
		dst.Close()
		return fmt.Errorf("archive: write %s: %w", target, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("archive: close %s: %w", target, err)
	}

	if err := os.Chmod(target, fileMode(mode)); err != nil {
		return fmt.Errorf("archive: chmod %s: %w", target, err)
	}
	return nil
}

// dirMode falls back to 0o755 if the ZIP entry has no permission bits.
func dirMode(m os.FileMode) os.FileMode {
	perm := m.Perm()
	if perm == 0 {
		return 0o755
	}
	return perm
}

// fileMode falls back to 0o644 if the ZIP entry has no permission bits.
func fileMode(m os.FileMode) os.FileMode {
	perm := m.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}
