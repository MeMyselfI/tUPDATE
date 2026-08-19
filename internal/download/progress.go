package download

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// progress renders the shared "\rDownload: ..." status line.
//
// All parts feed one atomic counter and a single ticker goroutine owns the
// printing. That keeps concurrent parts from interleaving output and makes the
// refresh rate independent of how chunks happen to arrive — with N parallel
// connections a print-on-read scheme would either stutter or spam.
type progress struct {
	sink  io.Writer
	total int64
	n     atomic.Int64
	start time.Time

	quit chan struct{}
	fin  chan struct{}
	once sync.Once

	// lastLen is the rune width of the previously printed line, used to pad
	// a shorter line so leftovers from the longer one are overwritten. Only
	// touched by the ticker goroutine and, after it has exited, by stop.
	lastLen int
}

// newProgress starts the ticker. A nil sink yields a live counter with no
// output, so callers need no nil checks. total <= 0 means "size unknown".
func newProgress(sink io.Writer, total int64) *progress {
	p := &progress{sink: sink, total: total, start: time.Now()}
	if sink == nil {
		return p
	}
	p.quit = make(chan struct{})
	p.fin = make(chan struct{})
	go func() {
		defer close(p.fin)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-p.quit:
				return
			case <-t.C:
				p.print()
			}
		}
	}()
	return p
}

func (p *progress) add(n int64) { p.n.Add(n) }

func (p *progress) count() int64 { return p.n.Load() }

// stop halts the ticker, prints one final line so the display always ends on
// the real byte count, and terminates it with a newline. Idempotent.
func (p *progress) stop() {
	p.once.Do(func() {
		if p.sink == nil {
			return
		}
		close(p.quit)
		<-p.fin
		p.print()
		fmt.Fprintln(p.sink)
	})
}

func (p *progress) print() {
	n := p.n.Load()
	elapsed := time.Since(p.start)

	var b strings.Builder
	fmt.Fprintf(&b, "Download: %s", humanBytes(n))
	if p.total > 0 {
		fmt.Fprintf(&b, " / %s (%.1f%%)", humanBytes(p.total), float64(n)/float64(p.total)*100)
	}
	// Below a quarter second the average is pure noise from connection setup.
	if elapsed > 250*time.Millisecond {
		fmt.Fprintf(&b, "  %s/s", humanBytes(int64(float64(n)/elapsed.Seconds())))
	}
	if eta, ok := p.eta(n, elapsed); ok {
		fmt.Fprintf(&b, "  ETA %s", eta)
	}

	line := b.String()
	if pad := p.lastLen - len([]rune(line)); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	p.lastLen = len([]rune(line))
	fmt.Fprint(p.sink, "\r"+line)
}

// eta extrapolates the remaining time from the average rate so far. It stays
// silent for the first seconds, where the average is still dominated by
// connection setup and would print a wildly wrong number.
func (p *progress) eta(n int64, elapsed time.Duration) (string, bool) {
	if p.total <= 0 || n <= 0 || n >= p.total || elapsed < 2*time.Second {
		return "", false
	}
	rate := float64(n) / elapsed.Seconds()
	return fmtDuration(time.Duration(float64(p.total-n) / rate * float64(time.Second))), true
}

// fmtDuration renders a countdown as m:ss, or h:mm:ss past the hour.
func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, (secs%3600)/60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// progressWriter counts the bytes actually written. Wrapping the destination
// (rather than the source) means a short write is never reported as progress,
// and it deliberately hides *os.File's ReadFrom so io.CopyBuffer really uses
// the large buffer it was handed.
type progressWriter struct {
	w io.Writer
	p *progress
}

func (pw *progressWriter) Write(b []byte) (int, error) {
	n, err := pw.w.Write(b)
	pw.p.add(int64(n))
	return n, err
}
