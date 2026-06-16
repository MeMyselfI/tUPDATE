package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownload_Success(t *testing.T) {
	payload := bytes.Repeat([]byte("xExam-Update-Data!"), 10000) // ~180 KB

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "180000")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	dest := filepath.Join(tmp, "out.zip")

	client, err := NewClient(10*time.Second, ProxyConfig{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := &Downloader{Client: client}

	n, err := d.Download(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("bytes written = %d, want %d", n, len(payload))
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch (lengths %d vs %d)", len(got), len(payload))
	}
}

func TestDownload_NotFoundReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := NewClient(10*time.Second, ProxyConfig{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := &Downloader{Client: client}

	_, err = d.Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x.zip"))
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestDownload_ContextCancelled(t *testing.T) {
	// Slow server that streams forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
				_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer srv.Close()

	client, err := NewClient(5*time.Second, ProxyConfig{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := &Downloader{Client: client}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err = d.Download(ctx, srv.URL, filepath.Join(t.TempDir(), "x.zip"))
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDownload_ProgressEmitted(t *testing.T) {
	payload := bytes.Repeat([]byte("p"), 32*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	client, err := NewClient(10*time.Second, ProxyConfig{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var buf bytes.Buffer
	d := &Downloader{Client: client, Progress: &buf}
	_, err = d.Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "p.zip"))
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	// Expect at least one progress line written (final EOF emits one).
	if buf.Len() == 0 {
		t.Error("expected progress output, got empty buffer")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		got := humanBytes(c.in)
		if got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
