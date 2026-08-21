//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// ownsConsole reports whether this process is the only one attached to the
// console window, which is how a double-clicked .exe looks: Explorer creates a
// fresh console just for us, and it disappears the moment we exit — taking any
// final output with it.
//
// Started from an existing cmd.exe / PowerShell / Windows Terminal the shell is
// attached too (count >= 2), so the window is not ours to close.
//
// A count of 0 means no console at all (GUI parent, detached run) — also not
// ours.
func ownsConsole() bool {
	var buf [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	return n == 1
}
