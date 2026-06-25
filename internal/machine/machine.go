// Package machine emits newline-delimited JSON events for Java / CI parsers.
//
// Output schema is intentionally tiny: every line is one self-contained JSON
// object with at least an "event" field. When the emitter is disabled (humans
// mode) every method is a no-op so callers can sprinkle Emit.* calls without
// guarding them at every call site.
package machine

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Emitter writes NDJSON events to a single writer. Construct with New; call
// Disabled to get a no-op emitter that satisfies the same interface so callers
// can stay branch-free.
type Emitter struct {
	mu      sync.Mutex
	w       io.Writer
	enabled bool
}

// New returns an emitter writing to w. Pass enabled=false to get a no-op
// emitter that still satisfies the method set.
func New(w io.Writer, enabled bool) *Emitter {
	return &Emitter{w: w, enabled: enabled}
}

// Enabled reports whether events would actually be written. Callers use this
// to short-circuit expensive payload preparation when no one is listening.
func (e *Emitter) Enabled() bool {
	if e == nil {
		return false
	}
	return e.enabled
}

// emit serializes obj as a single NDJSON line. obj must produce a top-level
// JSON object — callers always pass a map.
func (e *Emitter) emit(obj map[string]any) {
	if e == nil || !e.enabled {
		return
	}
	obj["ts"] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(obj)
	if err != nil {
		// Falling back to a synthetic error event keeps the stream parseable.
		fallback := fmt.Sprintf(`{"event":"emit_error","reason":%q,"ts":%q}`+"\n",
			err.Error(), time.Now().UTC().Format(time.RFC3339))
		e.mu.Lock()
		_, _ = e.w.Write([]byte(fallback))
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	_, _ = e.w.Write(data)
	_, _ = e.w.Write([]byte("\n"))
	e.mu.Unlock()
}

// Ready signals the start of a run. Callers emit this exactly once.
func (e *Emitter) Ready(version, logfile string) {
	e.emit(map[string]any{
		"event":   "ready",
		"version": version,
		"logfile": logfile,
	})
}

// Exit emits the terminal event before the process exits. code matches the
// process exit code so consumers can read either signal.
func (e *Emitter) Exit(code int) {
	e.emit(map[string]any{
		"event": "exit",
		"code":  code,
	})
}

// Detached is emitted by the parent half of --detach so callers learn the
// child PID + logfile path in machine-readable form.
func (e *Emitter) Detached(pid int, logfile string) {
	e.emit(map[string]any{
		"event":   "detached",
		"pid":     pid,
		"logfile": logfile,
	})
}

// DownloadStart fires when the HTTP fetch begins. url is the resolved URL.
func (e *Emitter) DownloadStart(url string) {
	e.emit(map[string]any{
		"event": "download_start",
		"url":   url,
	})
}

// DownloadDone fires after a successful download.
func (e *Emitter) DownloadDone(bytes int64) {
	e.emit(map[string]any{
		"event": "download_done",
		"bytes": bytes,
	})
}

// DownloadFailed surfaces a download error with a single reason field.
func (e *Emitter) DownloadFailed(reason string) {
	e.emit(map[string]any{
		"event":  "download_failed",
		"reason": reason,
	})
}

// ExtractDone fires once the ZIP is unpacked, with the (possibly wrapper-stripped)
// reference root that diffing will walk against the live install.
func (e *Emitter) ExtractDone(refRoot string) {
	e.emit(map[string]any{
		"event":    "extract_done",
		"ref_root": refRoot,
	})
}

// Diff carries the diff summary: per-directory counts and a grand total.
func (e *Emitter) Diff(added, modified, removed int, perDir map[string]map[string]int) {
	e.emit(map[string]any{
		"event":    "diff",
		"added":    added,
		"modified": modified,
		"removed":  removed,
		"per_dir":  perDir,
	})
}

// BackupFilesOK reports a successful ZIP backup.
func (e *Emitter) BackupFilesOK(path string, bytes int64) {
	e.emit(map[string]any{
		"event": "backup_files_ok",
		"path":  path,
		"bytes": bytes,
	})
}

// BackupFilesFailed reports a failed ZIP backup with a reason.
func (e *Emitter) BackupFilesFailed(reason string) {
	e.emit(map[string]any{
		"event":  "backup_files_failed",
		"reason": reason,
	})
}

// BackupDBOK reports a successful pg_dump.
func (e *Emitter) BackupDBOK(path string, bytes int64) {
	e.emit(map[string]any{
		"event": "backup_db_ok",
		"path":  path,
		"bytes": bytes,
	})
}

// BackupDBFailed reports pg_dump exiting non-zero or timing out.
func (e *Emitter) BackupDBFailed(reason string) {
	e.emit(map[string]any{
		"event":  "backup_db_failed",
		"reason": reason,
	})
}

// BackupDBSkipped fires when pg_dump could not be located (or the user
// declined the prompt — though in --json/--no-prompt the default is yes).
func (e *Emitter) BackupDBSkipped(reason string) {
	e.emit(map[string]any{
		"event":  "backup_db_skipped",
		"reason": reason,
	})
}

// ServiceStopOK / ServiceStopFailed cover the service-stop step.
func (e *Emitter) ServiceStopOK() {
	e.emit(map[string]any{"event": "service_stop_ok"})
}

func (e *Emitter) ServiceStopFailed(reason string) {
	e.emit(map[string]any{"event": "service_stop_failed", "reason": reason})
}

// ServiceStartOK / ServiceStartFailed cover the post-apply service-start step.
func (e *Emitter) ServiceStartOK() {
	e.emit(map[string]any{"event": "service_start_ok"})
}

func (e *Emitter) ServiceStartFailed(reason string) {
	e.emit(map[string]any{"event": "service_start_failed", "reason": reason})
}

// ApplyOK / ApplyFailed cover the file-sync step.
func (e *Emitter) ApplyOK() {
	e.emit(map[string]any{"event": "apply_ok"})
}

func (e *Emitter) ApplyFailed(reason string) {
	e.emit(map[string]any{"event": "apply_failed", "reason": reason})
}

// PreflightFailed reports that the pre-apply writability/lock check found one
// or more blocking targets, so the update was aborted before any mutation.
func (e *Emitter) PreflightFailed(count int) {
	e.emit(map[string]any{
		"event": "preflight_failed",
		"count": count,
	})
}

// DryRunCheck fires once per pre-flight check during --dry-run, carrying its
// name, ok-flag, and a free-text detail (path, status code, error message).
func (e *Emitter) DryRunCheck(name string, ok bool, detail string) {
	e.emit(map[string]any{
		"event":  "dry_run_check",
		"name":   name,
		"ok":     ok,
		"detail": detail,
	})
}

// FatalError is the last-resort event for errors the workflow can't classify
// into a more specific event (config parsing, paths, logfile).
func (e *Emitter) FatalError(stage, reason string) {
	e.emit(map[string]any{
		"event":  "fatal_error",
		"stage":  stage,
		"reason": reason,
	})
}
