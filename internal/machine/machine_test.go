package machine

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func parseEvents(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestEmitter_Disabled_NoOutput(t *testing.T) {
	var buf bytes.Buffer
	e := New(&buf, false)
	e.Ready("0.0.0", "/tmp/x")
	e.Exit(0)
	if buf.Len() != 0 {
		t.Errorf("disabled emitter should write nothing, got %q", buf.String())
	}
}

func TestEmitter_NilSafeNoPanic(t *testing.T) {
	var e *Emitter
	if e.Enabled() {
		t.Error("nil emitter should report disabled")
	}
	// All event methods should be safe on nil. We just call a representative
	// subset; if any branch is broken the test panics.
	e.Ready("0.0.0", "/tmp/x")
	e.Exit(0)
	e.DryRunCheck("x", true, "y")
}

func TestEmitter_EmitsNDJSONWithRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	e := New(&buf, true)
	e.Ready("1.2.3", "/var/log/u.log")
	e.DownloadStart("https://example.org/r.zip")
	e.DownloadDone(789963715)
	e.Diff(1, 2, 3, map[string]map[string]int{
		"bin": {"added": 0, "modified": 0, "removed": 1},
	})
	e.BackupFilesOK("/tmp/x.zip", 4096)
	e.BackupDBSkipped("pg_dump_not_found")
	e.Exit(0)

	events := parseEvents(t, buf.String())
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %v", len(events), events)
	}
	wantOrder := []string{"ready", "download_start", "download_done", "diff",
		"backup_files_ok", "backup_db_skipped", "exit"}
	for i, want := range wantOrder {
		if events[i]["event"] != want {
			t.Errorf("event[%d] = %v, want %s", i, events[i]["event"], want)
		}
		if _, ok := events[i]["ts"]; !ok {
			t.Errorf("event[%d] missing ts field", i)
		}
	}
	if events[3]["added"].(float64) != 1 {
		t.Errorf("diff.added = %v", events[3]["added"])
	}
	if perDir, ok := events[3]["per_dir"].(map[string]any); !ok {
		t.Errorf("diff.per_dir wrong shape: %v", events[3]["per_dir"])
	} else {
		bin, _ := perDir["bin"].(map[string]any)
		if bin["removed"].(float64) != 1 {
			t.Errorf("per_dir.bin.removed = %v", bin["removed"])
		}
	}
}

func TestEmitter_ConcurrentEmitsAreLineSafe(t *testing.T) {
	var buf bytes.Buffer
	e := New(&buf, true)
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			e.Ready("v", "logfile")
			e.Exit(0)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	// Every line must be valid JSON; we don't care about order, only that
	// we never see interleaved bytes.
	events := parseEvents(t, buf.String())
	if len(events) != 40 {
		t.Fatalf("expected 40 events, got %d", len(events))
	}
}

func TestEmitter_DiscardWriterTolerated(t *testing.T) {
	// Calling emit on io.Discard should not error. Verifies the no-write
	// branch is at least exercised.
	e := New(io.Discard, true)
	e.Ready("0", "")
	e.Exit(0)
}
