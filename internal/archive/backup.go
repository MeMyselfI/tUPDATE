package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// BackupTimestampFormat is the filename layout for backup ZIPs.
const BackupTimestampFormat = "2006-01-02-15-04-05"

// BackupDirs zips the given dirs (under appRoot) into backupDir/<timestamp>.zip.
// Dirs that don't exist are silently skipped. Symbolic links are skipped.
// Returns the absolute path of the created ZIP.
func BackupDirs(appRoot, backupDir string, dirs []string, ts time.Time) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("backup: mkdir %s: %w", backupDir, err)
	}

	name := ts.Format(BackupTimestampFormat) + ".zip"
	zipPath := filepath.Join(backupDir, name)

	out, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("backup: create %s: %w", zipPath, err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)

	for _, dir := range dirs {
		if err := addDirToZip(zw, appRoot, dir); err != nil {
			zw.Close()
			os.Remove(zipPath)
			return "", err
		}
	}

	if err := zw.Close(); err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("backup: close zip: %w", err)
	}

	return zipPath, nil
}

func addDirToZip(zw *zip.Writer, appRoot, dir string) error {
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

		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // skip symlinks etc.
		}

		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		zipName := filepath.ToSlash(rel)

		fi, err := d.Info()
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		header.Name = zipName
		header.Method = zip.Deflate
		if d.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		}

		w, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("backup: zip header %s: %w", zipName, err)
		}
		if d.IsDir() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("backup: open %s: %w", path, err)
		}
		defer src.Close()
		if _, err := io.Copy(w, src); err != nil {
			return fmt.Errorf("backup: write %s: %w", path, err)
		}
		return nil
	})
}
