//go:build windows

package i18n

import "syscall"

// detectFromOS reads the user's default Windows UI language. Windows command
// prompts typically leave LC_ALL/LC_MESSAGES/LANG unset, so env-based detection
// alone returns English even on a German system. GetUserDefaultUILanguage
// returns a LANGID whose low 10 bits are the primary language identifier; we
// map the ones the updater supports. Anything else returns false so the caller
// falls back to English.
func detectFromOS() (Lang, bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")
	r, _, _ := proc.Call()
	switch uint16(r) & 0x3FF {
	case 0x07: // LANG_GERMAN
		return LangDE, true
	case 0x0C: // LANG_FRENCH
		return LangFR, true
	case 0x09: // LANG_ENGLISH
		return LangEN, true
	}
	return LangEN, false
}
