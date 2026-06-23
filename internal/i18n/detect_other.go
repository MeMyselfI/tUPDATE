//go:build !windows

package i18n

// detectFromOS is a no-op on non-Windows systems, where locale information is
// reliably available through the environment variables inspected by
// detectFromEnv. Returning false makes Detect fall back to LangEN.
func detectFromOS() (Lang, bool) { return LangEN, false }
