//go:build !windows

package main

import "os"

// openLogShared opens (and truncates) the logfile for writing. On Unix the OS
// imposes no mandatory locks, so a plain open already lets other processes read
// or rotate the file while the updater runs.
func openLogShared(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}
