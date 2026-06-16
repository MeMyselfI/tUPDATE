package service

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRun_SuccessfulCommand(t *testing.T) {
	r := New(runtime.GOOS)
	var out bytes.Buffer
	r.Stdout = &out

	cmd := "echo hello-from-service"
	if err := r.Run(context.Background(), cmd, 5*time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "hello-from-service") {
		t.Errorf("stdout = %q, missing expected token", out.String())
	}
}

func TestRun_NonZeroExitReturnsError(t *testing.T) {
	r := New(runtime.GOOS)
	cmd := "false"
	if runtime.GOOS == "windows" {
		cmd = "exit 1"
	}
	err := r.Run(context.Background(), cmd, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func TestRun_TimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on Windows shells")
	}
	r := New(runtime.GOOS)
	err := r.Run(context.Background(), "sleep 5", 150*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error message = %q, want 'timed out'", err.Error())
	}
}

func TestRun_EmptyCommandReturnsError(t *testing.T) {
	r := New(runtime.GOOS)
	if err := r.Run(context.Background(), "   ", time.Second); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestWrap_PicksShellPerOS(t *testing.T) {
	name, args := wrap("windows", "foo bar")
	if name != "cmd" || len(args) != 2 || args[0] != "/C" || args[1] != "foo bar" {
		t.Errorf("windows wrap = %s %v", name, args)
	}

	for _, os := range []string{"linux", "darwin"} {
		name, args := wrap(os, "foo bar")
		if name != "/bin/sh" || len(args) != 2 || args[0] != "-c" || args[1] != "foo bar" {
			t.Errorf("%s wrap = %s %v", os, name, args)
		}
	}
}

func TestRun_CapturesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stderr redirection syntax differs on Windows")
	}
	r := New(runtime.GOOS)
	var stdout, stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr

	err := r.Run(context.Background(), "echo to-stderr >&2", 5*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "to-stderr") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
