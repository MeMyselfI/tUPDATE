package sync

import (
	"os"
	"path/filepath"
)

// Block is one diff entry that cannot be applied because its live-tree target
// is locked, read-only, or sits in a directory we cannot create files in.
type Block struct {
	Path   string // "dir/path" — same identifier Apply's onFile callback uses
	Reason string // human-readable cause (locked / permission / missing parent)
}

// Preflight verifies, without mutating anything, that every change in diffs can
// be applied to liveRoot. It is meant to run after the service is stopped and
// immediately before the first destructive write, so a file held open by an
// editor, a virus scanner, or a still-running service is caught up front
// instead of aborting Apply half-way and leaving a mixed install.
//
// Probes per change type:
//   - Modified: open the existing target O_WRONLY (no O_TRUNC) and close it. Go
//     opens with shared FILE_SHARE_* on Windows, so the probe never locks the
//     file itself; a foreign exclusive lock or a read-only bit makes the open
//     fail — the same open copyFile would later perform with O_TRUNC.
//   - Removed: the same O_WRONLY probe as a deletability proxy. A running .exe
//     or an otherwise locked file can be opened for write by no one, and on
//     Windows cannot be deleted either.
//   - Added: probe the nearest existing ancestor directory for "can create a
//     file here" by writing and deleting a throwaway probe file. Deduplicated
//     per directory so a big Added set does not re-probe the same dir.
//
// An empty result means Apply is expected to succeed. Targets that do not exist
// (a Modified/Removed entry already gone) are treated as non-blocking — Apply
// creates or skips them.
//
// Caveat: this cannot close the race against a scanner that grabs a file in the
// window between the probe and the real write. It deterministically catches the
// steady-state cases (editor holding a file open, service still running,
// read-only target); the transient-lock race needs apply-time retries on top.
func Preflight(liveRoot string, diffs []DirDiff) []Block {
	var blocks []Block
	dirChecked := make(map[string]string) // ancestor dir -> reason ("" = ok)

	for _, d := range diffs {
		for _, c := range d.Changes {
			name := d.Dir + "/" + c.Path
			target := filepath.Join(liveRoot, d.Dir, filepath.FromSlash(c.Path))

			switch c.Type {
			case Modified, Removed:
				if reason := probeFileWritable(target); reason != "" {
					blocks = append(blocks, Block{Path: name, Reason: reason})
				}
			case Added:
				dir := filepath.Dir(target)
				reason, seen := dirChecked[dir]
				if !seen {
					reason = probeDirWritable(dir)
					dirChecked[dir] = reason
				}
				if reason != "" {
					blocks = append(blocks, Block{Path: name, Reason: reason})
				}
			}
		}
	}
	return blocks
}

// probeFileWritable reports why target cannot be overwritten or deleted, or ""
// if it can. A missing file is not a blocker. Directories are skipped (diffs
// only carry regular files). The file is opened write-only without truncating,
// so existing content is never touched.
func probeFileWritable(target string) string {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "" // nothing here to lock; Apply will create/skip it
		}
		return "stat failed: " + err.Error()
	}
	if info.IsDir() {
		return ""
	}
	f, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return "not writable (locked / in use / read-only): " + err.Error()
	}
	_ = f.Close()
	return ""
}

// probeDirWritable reports why a new file cannot be created under dir, or "".
// Added paths may introduce new sub-directories that Apply would MkdirAll, so
// the probe walks up to the nearest existing ancestor and writes+removes a
// throwaway file there to prove create permission.
func probeDirWritable(dir string) string {
	anc := dir
	for {
		info, err := os.Stat(anc)
		if err == nil {
			if !info.IsDir() {
				return anc + " is not a directory"
			}
			break
		}
		if !os.IsNotExist(err) {
			return "stat " + anc + " failed: " + err.Error()
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			return "no existing ancestor directory for " + dir
		}
		anc = parent
	}

	// CreateTemp picks a unique name so a probe left behind by a crashed run
	// never causes a false "cannot create" via an O_EXCL name collision.
	f, err := os.CreateTemp(anc, ".tupdate-preflight-*")
	if err != nil {
		return "cannot create files in " + anc + ": " + err.Error()
	}
	probe := f.Name()
	_ = f.Close()
	_ = os.Remove(probe)
	return ""
}
