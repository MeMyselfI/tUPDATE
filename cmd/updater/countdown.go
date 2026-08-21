package main

import (
	"bufio"
	"fmt"
	"io"
	"time"
)

// countdownWidth pads the \r-redrawn line so a shrinking number ("10" -> "9")
// never leaves a stale character behind.
const countdownWidth = 72

// closeCountdown counts down secs seconds on w, redrawing a single \r line per
// tick, and returns as soon as either the countdown hits zero or something
// arrives on keys (the user pressed Enter to close immediately).
//
// ticks and keys are parameters so the tests can drive it without sleeping.
func closeCountdown(w io.Writer, keys <-chan struct{}, ticks <-chan time.Time, secs int, format string) {
	remaining := secs
	draw := func() {
		fmt.Fprintf(w, "\r%-*s", countdownWidth, fmt.Sprintf(format, remaining))
	}
	draw()
	for remaining > 0 {
		select {
		case <-keys:
			fmt.Fprintln(w)
			return
		case <-ticks:
			remaining--
			draw()
		}
	}
	fmt.Fprintln(w)
}

// waitBeforeClose runs the close countdown against the real clock and stdin.
//
// The stdin reader lives in a goroutine that is never joined: the process exits
// right after this returns, so a blocked read is fine.
func waitBeforeClose(w io.Writer, stdin io.Reader, secs int, format string) {
	if secs <= 0 {
		return
	}
	keys := make(chan struct{}, 1)
	go func() {
		br := bufio.NewReader(stdin)
		if _, err := br.ReadString('\n'); err == nil {
			keys <- struct{}{}
		}
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	closeCountdown(w, keys, t.C, secs, format)
}
