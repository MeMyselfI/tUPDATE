package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Downloader fetches a remote URL to a local file.
type Downloader struct {
	Client *http.Client
	// Progress, if non-nil, receives a "\r"-prefixed progress line
	// roughly every 200 ms during the transfer and a trailing newline at EOF.
	Progress io.Writer
}

// Download streams the response body of GET url into destPath.
// It overwrites destPath if it already exists.
// Returns the number of bytes written and any error encountered.
func (d *Downloader) Download(ctx context.Context, url, destPath string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("download: build request: %w", err)
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("download: create %s: %w", destPath, err)
	}
	defer f.Close()

	src := io.Reader(resp.Body)
	if d.Progress != nil {
		src = &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			sink:  d.Progress,
		}
	}

	n, err := io.Copy(f, src)
	if err != nil {
		return n, fmt.Errorf("download: stream: %w", err)
	}
	if err := f.Sync(); err != nil {
		return n, fmt.Errorf("download: sync %s: %w", destPath, err)
	}
	return n, nil
}

type progressReader struct {
	r     io.Reader
	total int64
	n     int64
	last  time.Time
	sink  io.Writer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.sink != nil {
		emit := err == io.EOF || time.Since(p.last) > 200*time.Millisecond
		if emit {
			p.printLine()
			p.last = time.Now()
		}
		if err == io.EOF {
			fmt.Fprintln(p.sink)
		}
	}
	return n, err
}

func (p *progressReader) printLine() {
	if p.total > 0 {
		pct := float64(p.n) / float64(p.total) * 100
		fmt.Fprintf(p.sink, "\rDownload: %s / %s (%.1f%%)", humanBytes(p.n), humanBytes(p.total), pct)
	} else {
		fmt.Fprintf(p.sink, "\rDownload: %s", humanBytes(p.n))
	}
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
