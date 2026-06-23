package archive

import "strings"

// BackupFormat selects the container/compression of a file backup.
type BackupFormat int

const (
	// FormatTarXz is a tar stream compressed with xz/LZMA2 (default).
	FormatTarXz BackupFormat = iota
	// FormatZip is a classic DEFLATE zip — universally openable, faster.
	FormatZip
)

// String returns the lowercase identifier used in CLI flags and filenames.
func (f BackupFormat) String() string {
	if f == FormatZip {
		return "zip"
	}
	return "tar.xz"
}

func (f BackupFormat) ext() string {
	if f == FormatZip {
		return ".zip"
	}
	return ".tar.xz"
}

// ParseBackupFormat maps "zip" / "tar.xz" (also "tarxz", "xz") to a BackupFormat.
// The bool is false for unrecognised input.
func ParseBackupFormat(s string) (BackupFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zip":
		return FormatZip, true
	case "tar.xz", "tarxz", "xz", "txz":
		return FormatTarXz, true
	}
	return FormatTarXz, false
}

// CompressionLevel selects a speed/ratio trade-off, mapped per format.
type CompressionLevel int

const (
	// LevelMin is the fastest, weakest setting.
	LevelMin CompressionLevel = iota
	// LevelDefault is a balanced setting (default).
	LevelDefault
	// LevelMax is the smallest output, slowest and most memory hungry.
	LevelMax
)

// String returns the lowercase identifier used in CLI flags.
func (l CompressionLevel) String() string {
	switch l {
	case LevelMin:
		return "min"
	case LevelMax:
		return "max"
	default:
		return "default"
	}
}

// ParseCompressionLevel maps "min" / "default" / "max" to a CompressionLevel.
// The bool is false for unrecognised input.
func ParseCompressionLevel(s string) (CompressionLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "min", "fast", "fastest":
		return LevelMin, true
	case "default", "balanced", "normal":
		return LevelDefault, true
	case "max", "best", "maximum":
		return LevelMax, true
	}
	return LevelDefault, false
}

// BackupOptions configures BackupDirs.
type BackupOptions struct {
	Format BackupFormat
	Level  CompressionLevel
}
