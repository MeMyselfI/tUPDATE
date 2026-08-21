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

// BackupDirs writes the given dirs (under appRoot) plus opts.Include into
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

	entries, total, err := collectEntries(appRoot, dirs, opts.Include, skipDir)
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
		err = writeZipBackup(out, entries, opts.Level, total, progress)
	default:
		err = writeTarXzBackup(out, entries, opts.Level, total, progress)
	}
	if err != nil {
		os.Remove(outPath)
		return "", err
	}
	return outPath, nil
}

// backupEntry is one resolved archive member: a directory marker or a regular
// file, already named relative to the app root.
type backupEntry struct {
	name  string // archive name, forward slashes, no trailing "/" for dirs
	path  string // source path on disk
	isDir bool
	info  fs.FileInfo
}

// collectEntries resolves the sync dirs and the extra include paths into a flat,
// de-duplicated list of archive members and the total number of file bytes.
//
// Resolving up front (rather than walking inside each writer) keeps the two
// format writers identical in behaviour and makes de-duplication possible: an
// include path may well sit inside a sync directory, and writing the same name
// twice would produce a broken archive.
func collectEntries(appRoot string, dirs, include []string, skipDir string) ([]backupEntry, int64, error) {
	var (
		entries []backupEntry
		total   int64
	)
	seen := make(map[string]bool)

	add := func(path string, d fs.DirEntry) error {
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // skip symlinks etc.
		}
		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if seen[name] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		seen[name] = true
		entries = append(entries, backupEntry{name: name, path: path, isDir: d.IsDir(), info: info})
		if !d.IsDir() {
			total += info.Size()
		}
		return nil
	}

	walk := func(srcRoot string) error {
		return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() && filepath.Clean(path) == skipDir {
				return filepath.SkipDir
			}
			return add(path, d)
		})
	}

	for _, dir := range dirs {
		srcRoot := filepath.Join(appRoot, filepath.FromSlash(dir))
		info, err := os.Lstat(srcRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, fmt.Errorf("backup: stat %s: %w", srcRoot, err)
		}
		if !info.IsDir() {
			return nil, 0, fmt.Errorf("backup: %s is not a directory", srcRoot)
		}
		if err := walk(srcRoot); err != nil {
			return nil, 0, err
		}
	}

	// Include entries may be files or directories. Unlike sync dirs, a plain
	// file is a legitimate entry here, and a missing path is never an error —
	// the list is a best-effort safety net, not part of the update contract.
	for _, inc := range include {
		src := filepath.Join(appRoot, filepath.FromSlash(inc))
		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, fmt.Errorf("backup: stat %s: %w", src, err)
		}
		if filepath.Clean(src) == skipDir {
			continue
		}
		if info.IsDir() {
			if err := walk(src); err != nil {
				return nil, 0, err
			}
			continue
		}
		if err := add(src, fs.FileInfoToDirEntry(info)); err != nil {
			return nil, 0, err
		}
	}

	return entries, total, nil
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

func writeTarXzBackup(out io.Writer, entries []backupEntry, level CompressionLevel, total int64, progress Progress) error {
	xw, err := xzConfig(level).NewWriter(out)
	if err != nil {
		return fmt.Errorf("backup: init xz: %w", err)
	}
	tw := tar.NewWriter(xw)

	var done int64
	for _, e := range entries {
		if err := addEntryToTar(tw, e, &done, total, progress); err != nil {
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

func writeZipBackup(out io.Writer, entries []backupEntry, level CompressionLevel, total int64, progress Progress) error {
	zw := zip.NewWriter(out)
	lvl := flateLevel(level)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, lvl)
	})

	var done int64
	for _, e := range entries {
		if err := addEntryToZip(zw, e, &done, total, progress); err != nil {
			zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: close zip: %w", err)
	}
	return nil
}

func addEntryToTar(tw *tar.Writer, e backupEntry, done *int64, total int64, progress Progress) error {
	hdr, err := tar.FileInfoHeader(e.info, "")
	if err != nil {
		return err
	}
	hdr.Name = e.name
	if e.isDir {
		hdr.Name += "/"
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: tar header %s: %w", e.name, err)
	}
	if e.isDir {
		return nil
	}
	return copyIntoArchive(tw, e, done, total, progress)
}

func addEntryToZip(zw *zip.Writer, e backupEntry, done *int64, total int64, progress Progress) error {
	hdr, err := zip.FileInfoHeader(e.info)
	if err != nil {
		return err
	}
	hdr.Name = e.name
	hdr.Method = zip.Deflate
	if e.isDir {
		hdr.Name += "/"
		hdr.Method = zip.Store
	}
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("backup: zip header %s: %w", e.name, err)
	}
	if e.isDir {
		return nil
	}
	return copyIntoArchive(w, e, done, total, progress)
}

// copyIntoArchive streams one regular file into the archive writer while
// reporting progress.
//
// Exactly e.info.Size() bytes are written: the tar header was emitted from the
// size stat'ed during collection, and tar.Writer rejects both a short and an
// over-long body. A file that grew in the meantime is truncated, one that
// shrank is zero-padded — either way the archive stays readable instead of
// failing the whole backup over a file that moved under us.
func copyIntoArchive(w io.Writer, e backupEntry, done *int64, total int64, progress Progress) error {
	src, err := os.Open(e.path)
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", e.path, err)
	}
	defer src.Close()

	cw := &countingWriter{w: w, done: done, total: total, progress: progress}
	n, err := io.CopyN(cw, src, e.info.Size())
	if err != nil && err != io.EOF {
		return fmt.Errorf("backup: write %s: %w", e.name, err)
	}
	if missing := e.info.Size() - n; missing > 0 {
		if _, err := io.CopyN(cw, zeroReader{}, missing); err != nil {
			return fmt.Errorf("backup: pad %s: %w", e.name, err)
		}
	}
	return nil
}

// zeroReader is an endless source of zero bytes used to pad a file that shrank
// between stat and copy.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// countingWriter forwards writes to the underlying archive writer while
// reporting cumulative progress. It counts uncompressed input bytes, which
// matches the up-front total from collectEntries.
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
