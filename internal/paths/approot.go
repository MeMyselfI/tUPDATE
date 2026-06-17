package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppRoot returns the application root directory.
//
// The updater binary is expected to live in <appRoot>/updater/, so AppRoot
// returns the parent of the executable's directory. With an installation at
// /opt/myapp/updater/updater, AppRoot returns /opt/myapp. Siblings of the
// updater/ subfolder (conf/, bin/, www/, etc/, libs/, backup/) are then
// resolvable relative to the returned path.
//
// Symlinks to the executable are resolved so the result is the real on-disk
// location, not the symlink target's parent.
func AppRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("paths: locate executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("paths: resolve symlink %s: %w", exe, err)
	}
	return filepath.Dir(filepath.Dir(resolved)), nil
}
