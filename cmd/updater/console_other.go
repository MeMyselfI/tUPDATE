//go:build !windows

package main

// ownsConsole is Windows-only behaviour: on Unix a terminal is never created
// for the process, so there is no window of ours to close.
func ownsConsole() bool { return false }
