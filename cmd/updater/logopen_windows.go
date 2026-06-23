//go:build windows

package main

import (
	"os"
	"syscall"
)

// openLogShared opens (and truncates) the logfile with a full Windows share
// mode — FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE — so the
// operator can tail, open in an editor, rename, or delete the log while the
// updater still holds it open. Go's default os.OpenFile omits FILE_SHARE_DELETE,
// which blocks renaming/deleting the active log on Windows.
func openLogShared(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.CREATE_ALWAYS, // create or truncate, matching O_CREATE|O_TRUNC
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
