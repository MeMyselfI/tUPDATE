package archive

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PgDumpTimeout is the hard timeout for the pg_dump subprocess.
const PgDumpTimeout = 30 * time.Minute

// PgDumpFileSuffix is appended to the backup timestamp to form the dump filename.
const PgDumpFileSuffix = "-db.backup"

// PgDumpOptions bundles all knobs RunPgDump needs.
//
// ExtraEnv entries (KEY=VAL) are appended to os.Environ(), so caller-supplied
// values win over the parent process environment. HasPassword toggles auto
// injection of -w (never prompt for password) when the caller is providing a
// password and the user hasn't already passed -w / --no-password in ExtraArgs.
type PgDumpOptions struct {
	Binary      string
	OutFile     string
	ExtraArgs   []string
	ExtraEnv    []string
	HasPassword bool
}

// PgDumpPath returns the absolute path the dump file will be written to,
// using the same timestamp as the ZIP backup so both artifacts pair up.
func PgDumpPath(backupDir string, ts time.Time) string {
	name := ts.Format(BackupTimestampFormat) + PgDumpFileSuffix
	return filepath.Join(backupDir, name)
}

// RunPgDump invokes pg_dump with -Fc -f <outFile> plus the caller's extra args.
// Connection parameters (host, port, user, password, database) can be supplied
// via opts.ExtraArgs, via opts.ExtraEnv (PGHOST/PGPORT/PGUSER/PGPASSWORD/
// PGDATABASE), or via ~/.pgpass.
//
// Stdout and stderr of the subprocess are streamed to the given log writer
// (usually the same io.MultiWriter the rest of the run uses).
func RunPgDump(ctx context.Context, opts PgDumpOptions, log io.Writer) error {
	if opts.Binary == "" {
		return fmt.Errorf("pgdump: binary path is empty")
	}
	if opts.OutFile == "" {
		return fmt.Errorf("pgdump: output file path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutFile), 0o755); err != nil {
		return fmt.Errorf("pgdump: mkdir %s: %w", filepath.Dir(opts.OutFile), err)
	}

	runCtx, cancel := context.WithTimeout(ctx, PgDumpTimeout)
	defer cancel()

	args := []string{"-Fc", "-f", opts.OutFile}
	if opts.HasPassword && !hasNoPasswordFlag(opts.ExtraArgs) {
		args = append(args, "-w")
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.CommandContext(runCtx, opts.Binary, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	if len(opts.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), opts.ExtraEnv...)
	}

	if err := cmd.Run(); err != nil {
		_ = os.Remove(opts.OutFile)
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("pgdump: timed out after %s", PgDumpTimeout)
		}
		return fmt.Errorf("pgdump: %s: %w", opts.Binary, err)
	}
	return nil
}

// hasNoPasswordFlag returns true if the user already passed -w / --no-password
// in their args, so we don't add a redundant -w.
func hasNoPasswordFlag(args []string) bool {
	for _, a := range args {
		if a == "-w" || a == "--no-password" || strings.HasPrefix(a, "--no-password=") {
			return true
		}
	}
	return false
}
