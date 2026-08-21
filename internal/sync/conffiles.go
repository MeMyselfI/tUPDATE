package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfState is the comparison result for one configuration file.
type ConfState int

const (
	// ConfSame means live and release copy are byte-identical — nothing to do.
	ConfSame ConfState = iota
	// ConfModified means the live copy differs from the release copy.
	ConfModified
	// ConfMissingLive means the file does not exist in the installation yet.
	ConfMissingLive
	// ConfMissingRef means the release does not ship this file. It is left
	// alone; a stale entry in conf.files must never delete a live config.
	ConfMissingRef
)

// ConfFile is one entry of the conf.files list together with its state.
type ConfFile struct {
	Path  string // app-root-relative, forward slashes
	State ConfState
}

// NeedsUpdate reports whether this entry would be written by ApplyConfFiles.
func (c ConfFile) NeedsUpdate() bool {
	return c.State == ConfModified || c.State == ConfMissingLive
}

// CompareConfFiles compares each configured file between the extracted release
// (refRoot) and the installation (liveRoot).
//
// These files sit outside the regular sync directories on purpose: they hold
// installation-specific settings, so the normal diff must never touch them.
// They are only ever written after an explicit confirmation, which is why the
// comparison is a separate, much simpler pass than Compute — no tree walk, no
// worker pool, just the handful of paths from the config.
func CompareConfFiles(refRoot, liveRoot string, files []string) ([]ConfFile, error) {
	if len(files) == 0 {
		return nil, nil
	}
	bufs := newCompareBuffers()
	out := make([]ConfFile, 0, len(files))

	for _, rel := range files {
		native := filepath.FromSlash(rel)
		refPath := filepath.Join(refRoot, native)
		livePath := filepath.Join(liveRoot, native)

		refInfo, err := os.Lstat(refPath)
		if err != nil || !refInfo.Mode().IsRegular() {
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("conf: stat %s: %w", refPath, err)
			}
			out = append(out, ConfFile{Path: rel, State: ConfMissingRef})
			continue
		}

		liveInfo, err := os.Lstat(livePath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("conf: stat %s: %w", livePath, err)
			}
			out = append(out, ConfFile{Path: rel, State: ConfMissingLive})
			continue
		}
		if !liveInfo.Mode().IsRegular() {
			// A directory or symlink where a config file belongs is not
			// something an updater should silently replace.
			return nil, fmt.Errorf("conf: %s is not a regular file", livePath)
		}

		same, err := sameContent(refPath, refInfo, livePath, liveInfo, bufs)
		if err != nil {
			return nil, fmt.Errorf("conf: compare %s: %w", rel, err)
		}
		state := ConfModified
		if same {
			state = ConfSame
		}
		out = append(out, ConfFile{Path: rel, State: state})
	}
	return out, nil
}

// ConfUpdateCount returns how many entries ApplyConfFiles would write.
func ConfUpdateCount(files []ConfFile) int {
	n := 0
	for _, f := range files {
		if f.NeedsUpdate() {
			n++
		}
	}
	return n
}

// ApplyConfFiles overwrites the live copies of every entry that differs from
// the release. Identical files and files the release does not ship are left
// untouched. If onFile is non-nil it is called with each path just before it
// is written.
func ApplyConfFiles(refRoot, liveRoot string, files []ConfFile, onFile func(name string)) error {
	for _, f := range files {
		if !f.NeedsUpdate() {
			continue
		}
		if onFile != nil {
			onFile(f.Path)
		}
		native := filepath.FromSlash(f.Path)
		if err := copyFile(filepath.Join(refRoot, native), filepath.Join(liveRoot, native)); err != nil {
			return fmt.Errorf("conf: %s: %w", f.Path, err)
		}
	}
	return nil
}

// PreflightConfFiles runs the same writability probes as Preflight against the
// conf files that would be overwritten, so a locked or read-only config aborts
// the run before anything is mutated.
func PreflightConfFiles(liveRoot string, files []ConfFile) []Block {
	var blocks []Block
	for _, f := range files {
		if !f.NeedsUpdate() {
			continue
		}
		target := filepath.Join(liveRoot, filepath.FromSlash(f.Path))
		var reason string
		if f.State == ConfMissingLive {
			reason = probeDirWritable(filepath.Dir(target))
		} else {
			reason = probeFileWritable(target)
		}
		if reason != "" {
			blocks = append(blocks, Block{Path: f.Path, Reason: reason})
		}
	}
	return blocks
}
