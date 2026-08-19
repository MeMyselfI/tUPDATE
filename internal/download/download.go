package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultParts is the number of parallel range requests used when the
	// server supports them and the payload is large enough to be worth
	// splitting.
	DefaultParts = 4
	// MaxParts caps the configured part count so a typo cannot open hundreds
	// of connections against the release server.
	MaxParts = 16
	// minPartSize is the smallest slice worth its own connection. Below this,
	// connection setup costs more than the parallelism saves.
	minPartSize = 8 << 20
	// copyBufSize replaces io.Copy's 32 KiB default. Fewer, larger write
	// syscalls measurably reduce transfer time, most noticeably on Windows.
	copyBufSize = 1 << 20
	// partAttempts is the number of tries per range slice. A retry resumes at
	// the offset the previous attempt reached, so nothing already on disk is
	// fetched twice.
	partAttempts = 3
)

// errNoRangeSupport marks a server that answered a mid-transfer range request
// with something other than 206. Retrying cannot fix that, so the part fails
// immediately instead of burning its attempts.
var errNoRangeSupport = errors.New("server stopped honouring Range requests")

// Downloader fetches a remote URL to a local file.
type Downloader struct {
	Client *http.Client
	// Progress, if non-nil, receives a "\r"-prefixed progress line roughly
	// every 200 ms during the transfer and a trailing newline at the end.
	Progress io.Writer
	// Parts is the maximum number of parallel range requests. Zero selects
	// DefaultParts; 1 forces the single-stream path. A server without range
	// support, or a payload of unknown or small size, also falls back to a
	// single stream.
	Parts int
}

// Download streams GET url into destPath, overwriting an existing file.
// Returns the number of bytes written and any error encountered.
func (d *Downloader) Download(ctx context.Context, url, destPath string) (int64, error) {
	size, ranged, reuse, err := d.probe(ctx, url)
	if err != nil {
		return 0, err
	}

	parts := d.planParts(size, ranged)
	if parts <= 1 {
		return d.serial(ctx, url, destPath, size, reuse)
	}
	if reuse != nil {
		reuse.Body.Close()
	}
	return d.parallel(ctx, url, destPath, size, parts)
}

// probe issues a GET for the first byte only. A 206 answer proves the server
// honours Range requests and its Content-Range header carries the total size.
// A 200 answer means the header was ignored and the returned response already
// holds the complete body, which the single-stream path then consumes instead
// of paying for a second request.
//
// The returned response is non-nil only when it is reusable that way; the
// caller owns closing it.
func (d *Downloader) probe(ctx context.Context, url string) (size int64, ranged bool, reuse *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, nil, fmt.Errorf("download: build request: %w", err)
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, false, nil, fmt.Errorf("download: request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return 0, false, nil, fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}

	if resp.StatusCode == http.StatusPartialContent {
		total, ok := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		resp.Body.Close()
		if !ok {
			// Ranges work but the total size is unknown, so the payload
			// cannot be sliced. Fall back to a fresh single-stream GET.
			return 0, false, nil, nil
		}
		return total, true, nil, nil
	}

	// Range ignored: this response is the whole file.
	return resp.ContentLength, false, resp, nil
}

// planParts decides how many parallel range requests to run. Anything the
// server or the payload does not clearly support collapses to 1.
func (d *Downloader) planParts(size int64, ranged bool) int {
	if !ranged || size <= 0 {
		return 1
	}
	parts := d.Parts
	if parts <= 0 {
		parts = DefaultParts
	}
	if parts > MaxParts {
		parts = MaxParts
	}
	if maxParts := size / minPartSize; int64(parts) > maxParts {
		parts = int(maxParts)
	}
	if parts < 1 {
		parts = 1
	}
	return parts
}

// serial is the single-stream path. reuse, when non-nil, is a response whose
// body is the complete payload (see probe); otherwise a fresh GET is issued.
func (d *Downloader) serial(ctx context.Context, url, destPath string, size int64, reuse *http.Response) (int64, error) {
	resp := reuse
	if resp == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, fmt.Errorf("download: build request: %w", err)
		}
		resp, err = d.Client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("download: request: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return 0, fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
		}
		size = resp.ContentLength
	}
	defer resp.Body.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("download: create %s: %w", destPath, err)
	}

	prog := newProgress(d.Progress, size)
	buf := make([]byte, copyBufSize)
	n, copyErr := io.CopyBuffer(&progressWriter{w: f, p: prog}, resp.Body, buf)
	prog.stop()

	closeErr := f.Close()
	if copyErr != nil {
		return n, fmt.Errorf("download: stream: %w", copyErr)
	}
	if closeErr != nil {
		return n, fmt.Errorf("download: close %s: %w", destPath, closeErr)
	}
	return n, nil
}

// parallel preallocates destPath and fills disjoint byte ranges from parts
// concurrent connections. Every part writes at its own offset, so no ordering
// or buffering between them is needed.
func (d *Downloader) parallel(ctx context.Context, url, destPath string, size int64, parts int) (int64, error) {
	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("download: create %s: %w", destPath, err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return 0, fmt.Errorf("download: preallocate %s: %w", destPath, err)
	}

	prog := newProgress(d.Progress, size)

	// The first failing part cancels the others: without this they would keep
	// pulling megabytes for a transfer that is already doomed.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, parts)
	chunk := size / int64(parts)
	for i := 0; i < parts; i++ {
		start := int64(i) * chunk
		end := start + chunk - 1
		if i == parts-1 {
			end = size - 1 // the last part absorbs the division remainder
		}
		wg.Add(1)
		go func(idx int, start, end int64) {
			defer wg.Done()
			if err := d.fetchPart(ctx, url, f, start, end, prog); err != nil {
				errs[idx] = err
				cancel()
			}
		}(i, start, end)
	}
	wg.Wait()
	prog.stop()

	closeErr := f.Close()
	for _, e := range errs {
		if e != nil {
			return prog.count(), e
		}
	}
	if closeErr != nil {
		return prog.count(), fmt.Errorf("download: close %s: %w", destPath, closeErr)
	}
	return size, nil
}

// fetchPart downloads [start,end] into f, retrying a mid-stream failure from
// the offset already reached. A flaky connection therefore costs the remainder
// of one slice, not the whole download.
func (d *Downloader) fetchPart(ctx context.Context, url string, f *os.File, start, end int64, prog *progress) error {
	off := start
	var last error
	for attempt := 0; attempt < partAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}

		n, err := d.fetchRange(ctx, url, f, off, end, prog)
		off += n
		switch {
		case err == nil && off == end+1:
			return nil
		case err == nil:
			last = fmt.Errorf("download: range %d-%d: short body, %d bytes missing", start, end, end+1-off)
		case errors.Is(err, errNoRangeSupport):
			return err
		default:
			last = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}

// fetchRange performs one ranged GET and writes the body at its file offset.
// The body is clipped to the requested length so a server that overshoots the
// range cannot corrupt the neighbouring slice.
func (d *Downloader) fetchRange(ctx context.Context, url string, f *os.File, start, end int64, prog *progress) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("download: build request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download: range %d-%d: %w", start, end, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("download: range %d-%d: %w (status %d)", start, end, errNoRangeSupport, resp.StatusCode)
	}

	w := &progressWriter{w: io.NewOffsetWriter(f, start), p: prog}
	buf := make([]byte, copyBufSize)
	n, err := io.CopyBuffer(w, io.LimitReader(resp.Body, end-start+1), buf)
	if err != nil {
		return n, fmt.Errorf("download: range %d-%d: %w", start, end, err)
	}
	return n, nil
}

// parseContentRangeTotal extracts the total size from a "bytes 0-0/12345"
// Content-Range header. A "*" total (size unknown to the server) reports false.
func parseContentRangeTotal(v string) (int64, bool) {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
