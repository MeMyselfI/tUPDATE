package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

// BackupTimestampFormat is the filename layout for backup archives.
const BackupTimestampFormat = "2006-01-02-15-04-05"

// Progress is invoked while file data is streamed into the archive. done is the
// number of (uncompressed) bytes copied so far, total the sum of all regular
// file sizes computed up front. progress callbacks may be nil.
type Progress func(done, total int64)

// BackupDirs writes the given dirs (under appRoot) into
// backupDir/<timestamp><ext> using the format and compression level in opts.
// Dirs that don't exist are silently skipped. Symbolic links are skipped.
// If progress is non-nil it is called as bytes are processed.
// Returns the absolute path of the created archive.
func BackupDirs(appRoot, backupDir string, dirs []string, ts time.Time, opts BackupOptions, progress Progress) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("backup: mkdir %s: %w", backupDir, err)
	}

	// Never walk into the backup directory itself: if it lives inside one of the
	// sync dirs, the running backup would otherwise try to archive the file it
	// is currently writing — an ever-growing file that never finishes.
	skipDir := filepath.Clean(backupDir)

	total, err := totalBytes(appRoot, dirs, skipDir)
	if err != nil {
		return "", err
	}

	name := ts.Format(BackupTimestampFormat) + opts.Format.ext()
	outPath := filepath.Join(backupDir, name)

	out, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("backup: create %s: %w", outPath, err)
	}
	defer out.Close()

	if progress != nil {
		progress(0, total)
	}

	switch opts.Format {
	case FormatZip:
		err = writeZipBackup(out, appRoot, dirs, skipDir, opts.Level, total, progress)
	default:
		err = writeTarXzBackup(out, appRoot, dirs, skipDir, opts.Level, total, progress)
	}
	if err != nil {
		os.Remove(outPath)
		return "", err
	}
	return outPath, nil
}

// xzConfig maps a CompressionLevel to xz/LZMA2 writer parameters. min and
// default use the fast HashTable matcher; max uses BinaryTree with a 64 MiB
// dictionary (xz -9 class) — best ratio, slowest, ~0.7 GB encoder RAM.
func xzConfig(level CompressionLevel) xz.WriterConfig {
	switch level {
	case LevelMin:
		return xz.WriterConfig{DictCap: 1 << 20, Matcher: lzma.HashTable4} // 1 MiB
	case LevelMax:
		return xz.WriterConfig{DictCap: 1 << 26, Matcher: lzma.BinaryTree} // 64 MiB
	default:
		return xz.WriterConfig{DictCap: 1 << 23, Matcher: lzma.HashTable4} // 8 MiB
	}
}

// flateLevel maps a CompressionLevel to a compress/flate level for zip backups.
func flateLevel(level CompressionLevel) int {
	switch level {
	case LevelMin:
		return flate.BestSpeed
	case LevelMax:
		return flate.BestCompression
	default:
		return flate.DefaultCompression
	}
}

func writeTarXzBackup(out io.Writer, appRoot string, dirs []string, skipDir string, level CompressionLevel, total int64, progress Progress) error {
	xw, err := xzConfig(level).NewWriter(out)
	if err != nil {
		return fmt.Errorf("backup: init xz: %w", err)
	}
	tw := tar.NewWriter(xw)

	var done int64
	for _, dir := range dirs {
		if err := addDirToTar(tw, appRoot, dir, skipDir, &done, total, progress); err != nil {
			tw.Close()
			xw.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		xw.Close()
		return fmt.Errorf("backup: close tar: %w", err)
	}
	if err := xw.Close(); err != nil {
		return fmt.Errorf("backup: close xz: %w", err)
	}
	return nil
}

func writeZipBackup(out io.Writer, appRoot string, dirs []string, skipDir string, level CompressionLevel, total int64, progress Progress) error {
	zw := zip.NewWriter(out)
	lvl := flateLevel(level)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, lvl)
	})

	var done int64
	for _, dir := range dirs {
		if err := addDirToZip(zw, appRoot, dir, skipDir, &done, total, progress); err != nil {
			zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: close zip: %w", err)
	}
	return nil
}

// totalBytes sums the sizes of all regular files under the given dirs so the
// progress callback has a denominator. Missing dirs and skipDir are skipped.
func totalBytes(appRoot string, dirs []string, skipDir string) (int64, error) {
	var total int64
	for _, dir := range dirs {
		srcRoot := filepath.Join(appRoot, dir)
		if _, err := os.Lstat(srcRoot); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("backup: stat %s: %w", srcRoot, err)
		}
		err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() && filepath.Clean(path) == skipDir {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() {
				fi, err := d.Info()
				if err != nil {
					return err
				}
				total += fi.Size()
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func addDirToTar(tw *tar.Writer, appRoot, dir, skipDir string, done *int64, total int64, progress Progress) error {
	srcRoot := filepath.Join(appRoot, dir)
	info, err := os.Lstat(srcRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("backup: stat %s: %w", srcRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup: %s is not a directory", srcRoot)
	}

	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() && filepath.Clean(path) == skipDir {
			return filepath.SkipDir
		}

		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // skip symlinks etc.
		}

		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		fi, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if d.IsDir() {
			hdr.Name += "/"
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("backup: tar header %s: %w", name, err)
		}
		if d.IsDir() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("backup: open %s: %w", path, err)
		}
		defer src.Close()

		cw := &countingWriter{w: tw, done: done, total: total, progress: progress}
		if _, err := io.Copy(cw, src); err != nil {
			return fmt.Errorf("backup: write %s: %w", name, err)
		}
		return nil
	})
}

func addDirToZip(zw *zip.Writer, appRoot, dir, skipDir string, done *int64, total int64, progress Progress) error {
	srcRoot := filepath.Join(appRoot, dir)
	info, err := os.Lstat(srcRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("backup: stat %s: %w", srcRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup: %s is not a directory", srcRoot)
	}

	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() && filepath.Clean(path) == skipDir {
			return filepath.SkipDir
		}

		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // skip symlinks etc.
		}

		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		fi, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		if d.IsDir() {
			hdr.Name += "/"
			hdr.Method = zip.Store
		}

		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("backup: zip header %s: %w", name, err)
		}
		if d.IsDir() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("backup: open %s: %w", path, err)
		}
		defer src.Close()

		cw := &countingWriter{w: w, done: done, total: total, progress: progress}
		if _, err := io.Copy(cw, src); err != nil {
			return fmt.Errorf("backup: write %s: %w", name, err)
		}
		return nil
	})
}

// countingWriter forwards writes to the underlying archive writer while
// reporting cumulative progress. It counts uncompressed input bytes, which
// matches the up-front total from totalBytes.
type countingWriter struct {
	w        io.Writer
	done     *int64
	total    int64
	progress Progress
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 && c.progress != nil {
		*c.done += int64(n)
		c.progress(*c.done, c.total)
	}
	return n, err
}
