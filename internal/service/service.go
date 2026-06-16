package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Runner executes shell commands with a timeout and OS-aware wrapper.
type Runner struct {
	// GOOS is the runtime os string used to pick the shell wrapper.
	GOOS string
	// Stdout/Stderr receive the command output. nil discards.
	Stdout io.Writer
	Stderr io.Writer
}

// New creates a Runner targeting the given GOOS.
// Output is discarded by default; set Stdout/Stderr after construction to capture.
func New(goos string) *Runner {
	return &Runner{GOOS: goos}
}

// Run executes command with the given timeout.
// Returns an error if the wrapper invocation fails, the command exits non-zero, or the timeout fires.
func (r *Runner) Run(ctx context.Context, command string, timeout time.Duration) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("service: empty command")
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, args := wrap(r.GOOS, command)

	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	err := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("service: command timed out after %s: %s", timeout, command)
	}
	if err != nil {
		return fmt.Errorf("service: command failed: %s: %w", command, err)
	}
	return nil
}

// wrap returns the shell command + args to run the user command.
// Windows → cmd /C <command>
// Unix    → sh /bin/sh -c <command>
func wrap(goos, command string) (string, []string) {
	if goos == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "/bin/sh", []string{"-c", command}
}
