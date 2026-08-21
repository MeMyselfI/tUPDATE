package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const testFormat = "closing in %d s"

func TestCloseCountdown_RunsToZero(t *testing.T) {
	var buf bytes.Buffer
	ticks := make(chan time.Time, 3)
	for i := 0; i < 3; i++ {
		ticks <- time.Now()
	}
	closeCountdown(&buf, make(chan struct{}), ticks, 3, testFormat)

	out := buf.String()
	for _, want := range []string{"closing in 3 s", "closing in 2 s", "closing in 1 s", "closing in 0 s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%q", want, out)
		}
	}
	if got := strings.Count(out, "\r"); got != 4 {
		t.Fatalf("want 4 redraws, got %d: %q", got, out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("countdown must end the line: %q", out)
	}
}

func TestCloseCountdown_KeyEndsItEarly(t *testing.T) {
	var buf bytes.Buffer
	keys := make(chan struct{}, 1)
	keys <- struct{}{}

	done := make(chan struct{})
	go func() {
		closeCountdown(&buf, keys, make(chan time.Time), 10, testFormat)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("key press did not end the countdown")
	}

	out := buf.String()
	if !strings.Contains(out, "closing in 10 s") {
		t.Fatalf("initial frame missing: %q", out)
	}
	if strings.Contains(out, "closing in 9 s") {
		t.Fatalf("countdown kept ticking after key press: %q", out)
	}
}

func TestWaitBeforeClose_ZeroDelayIsNoop(t *testing.T) {
	var buf bytes.Buffer
	waitBeforeClose(&buf, strings.NewReader(""), 0, testFormat)
	if buf.Len() != 0 {
		t.Fatalf("--close-delay 0 must print nothing, got %q", buf.String())
	}
}

func TestWaitBeforeClose_ReturnsOnStdinLine(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		// 600 s would never elapse in a test; the stdin line must end it.
		waitBeforeClose(&buf, strings.NewReader("\n"), 600, testFormat)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("waitBeforeClose ignored the stdin line")
	}
}
