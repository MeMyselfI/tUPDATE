package archive

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// PgDumpTimeout is the hard timeout for the pg_dump subprocess.
const PgDumpTimeout = 30 * time.Minute

// PgDumpFileSuffix is appended to the backup timestamp to form the dump filename.
const PgDumpFileSuffix = "-db.backup"

// PgDumpPath returns the absolute path the dump file will be written to,
// using the same timestamp as the ZIP backup so both artifacts pair up.
func PgDumpPath(backupDir string, ts time.Time) string {
	name := ts.Format(BackupTimestampFormat) + PgDumpFileSuffix
	return filepath.Join(backupDir, name)
}

// RunPgDump invokes pg_dump with -Fc -f <outFile> plus the user-supplied extraArgs.
// Connection parameters (host, port, user, password, database) must be supplied
// either via extraArgs or via libpq environment variables (PGHOST, PGPORT,
// PGUSER, PGPASSWORD, PGDATABASE, ~/.pgpass).
//
// Stdout and stderr of the subprocess are streamed to the given log writer
// (usually the same io.MultiWriter the rest of the run uses).
func RunPgDump(ctx context.Context, binary, outFile string, extraArgs []string, log io.Writer) error {
	if binary == "" {
		return fmt.Errorf("pgdump: binary path is empty")
	}
	if outFile == "" {
		return fmt.Errorf("pgdump: output file path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		return fmt.Errorf("pgdump: mkdir %s: %w", filepath.Dir(outFile), err)
	}

	runCtx, cancel := context.WithTimeout(ctx, PgDumpTimeout)
	defer cancel()

	args := append([]string{"-Fc", "-f", outFile}, extraArgs...)
	cmd := exec.CommandContext(runCtx, binary, args...)
	cmd.Stdout = log
	cmd.Stderr = log

	if err := cmd.Run(); err != nil {
		_ = os.Remove(outFile)
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("pgdump: timed out after %s", PgDumpTimeout)
		}
		return fmt.Errorf("pgdump: %s: %w", binary, err)
	}
	return nil
}
