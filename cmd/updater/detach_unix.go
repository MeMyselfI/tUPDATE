//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// spawnDetached starts binary with the given args as a detached process and
// returns its PID. The child is wired to /dev/null on all three standard
// streams and placed into its own session via setsid(2), so a SIGHUP from a
// disappearing controlling terminal does not propagate to it.
//
// The child is expected to open its own logfile via the --logfile flag the
// caller put into args, so nothing else needs to flow back to the parent.
func spawnDetached(binary string, args []string) (int, error) {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devnull.Close()

	attr := &os.ProcAttr{
		Files: []*os.File{devnull, devnull, devnull},
		Sys: &syscall.SysProcAttr{
			Setsid: true,
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
