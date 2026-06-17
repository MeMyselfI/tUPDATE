//go:build windows

package main

import (
	"os"
	"syscall"
)

// Windows CreateProcess flags we need to break the child out of the parent's
// process group and console. See:
// https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// spawnDetached starts binary as a detached process whose stdin/stdout/stderr
// are wired to "nul", outside the parent's process group and console.
//
// This is what survives a Service Control Manager "net stop" on the parent
// service: the child is no longer part of the service's job tree, so the SCM
// does not include it in the termination set.
func spawnDetached(binary string, args []string) (int, error) {
	nul, err := os.OpenFile("nul", os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer nul.Close()

	attr := &os.ProcAttr{
		Files: []*os.File{nul, nul, nul},
		Sys: &syscall.SysProcAttr{
			CreationFlags: detachedProcess | createNewProcessGroup,
		},
	}
	p, err := os.StartProcess(binary, append([]string{binary}, args...), attr)
	if err != nil {
		return 0, err
	}
	pid := p.Pid
	if err := p.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}
