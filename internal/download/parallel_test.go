package download

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rangeServer serves payload with full HTTP Range support and counts how many
// ranged requests it answered, so a test can prove the transfer was actually
// split.
func rangeServer(t *testing.T, payload []byte, ranged *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Range")
		if hdr == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
			return
		}
		start, end, ok := parseRangeHeader(hdr, int64(len(payload)))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if ranged != nil {
			ranged.Add(1)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
}

// parseRangeHeader understands the only form the downloader ever sends:
// "bytes=<start>-<end>".
func parseRangeHeader(h string, size int64) (int64, int64, bool) {
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return 0, 0, false
	}
	lo, hi, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(lo, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.ParseInt(hi, 10, 64)
	if err != nil || start > end || start >= size {
		return 0, 0, false
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true
}

func randomPayload(n int) []byte {
	b := make([]byte, n)
	rng := rand.New(rand.NewSource(1))
	_, _ = rng.Read(b)
	return b
}

func TestDownload_ParallelReassemblesPayload(t *testing.T) {
	// Four parts × minPartSize is the threshold for a 4-way split.
	payload := randomPayload(4 * minPartSize)

	var ranged atomic.Int64
	srv := rangeServer(t, payload, &ranged)
	defer srv.Close()

	client, err := NewClient(30*time.Second, ProxyConfig{}, ClientOptions{Parts: 4})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	d := &Downloader{Client: client, Parts: 4}

	n, err := d.Download(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("bytes = %d, want %d", n, len(payload))
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch (len %d vs %d)", len(got), len(payload))
	}
	// 1 probe + 4 slices.
	if ranged.Load() != 5 {
		t.Errorf("ranged requests = %d, want 5 (1 probe + 4 parts)", ranged.Load())
	}
}

func TestDownload_SmallPayloadStaysSingleStream(t *testing.T) {
	payload := randomPayload(64 << 10) // far below minPartSize

	var ranged atomic.Int64
	srv := rangeServer(t, payload, &ranged)
	defer srv.Close()

	client, err := NewClient(10*time.Second, ProxyConfig{}, ClientOptions{Parts: 4})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	d := &Downloader{Client: client, Parts: 4}

	if _, err := d.Download(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch (len %d vs %d)", len(got), len(payload))
	}
	// Only the probe may be ranged; the body itself comes as one stream.
	if ranged.Load() != 1 {
		t.Errorf("ranged requests = %d, want 1 (probe only)", ranged.Load())
	}
}

// A server that ignores Range must still yield a correct file: the probe
// response is the whole body and gets reused instead of re-requested.
func TestDownload_ServerWithoutRangeFallsBackToSingleStream(t *testing.T) {
	payload := randomPayload(4 * minPartSize)

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	client, err := NewClient(30*time.Second, ProxyConfig{}, ClientOptions{Parts: 4})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	d := &Downloader{Client: client, Parts: 4}

	n, err := d.Download(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("bytes = %d, want %d", n, len(payload))
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1 (probe body reused)", requests.Load())
	}
}

// A part whose connection dies mid-body must be retried from the offset it
// reached, not from the start of the slice, and the file must still be exact.
func TestDownload_PartRetryResumesAtOffset(t *testing.T) {
	payload := randomPayload(4 * minPartSize)

	var truncated atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, ok := parseRangeHeader(r.Header.Get("Range"), int64(len(payload)))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		body := payload[start : end+1]
		// Cut exactly one response short, once, to force a single retry.
		if len(body) > 1024 && truncated.CompareAndSwap(false, true) {
			body = body[:len(body)/2]
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client, err := NewClient(30*time.Second, ProxyConfig{}, ClientOptions{Parts: 4})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	d := &Downloader{Client: client, Parts: 4}

	if _, err := d.Download(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch after retry")
	}
	if !truncated.Load() {
		t.Error("test did not exercise the truncation path")
	}
}

func TestPlanParts(t *testing.T) {
	cases := []struct {
		name   string
		parts  int
		size   int64
		ranged bool
		want   int
	}{
		{"no range support", 4, 100 * minPartSize, false, 1},
		{"unknown size", 4, -1, true, 1},
		{"too small to split", 4, minPartSize - 1, true, 1},
		{"exactly two slices", 4, 2 * minPartSize, true, 2},
		{"full split", 4, 100 * minPartSize, true, 4},
		{"zero means default", 0, 100 * minPartSize, true, DefaultParts},
		{"clamped to MaxParts", 999, 1000 * minPartSize, true, MaxParts},
		{"explicit single stream", 1, 100 * minPartSize, true, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &Downloader{Parts: c.parts}
			if got := d.planParts(c.size, c.ranged); got != c.want {
				t.Errorf("planParts(%d, %v) = %d, want %d", c.size, c.ranged, got, c.want)
			}
		})
	}
}

func TestParseContentRangeTotal(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"bytes 0-0/12345", 12345, true},
		{"bytes 0-0/*", 0, false},
		{"bytes 0-0", 0, false},
		{"", 0, false},
		{"bytes 0-0/0", 0, false},
	}
	for _, c := range cases {
		got, ok := parseContentRangeTotal(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseContentRangeTotal(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
