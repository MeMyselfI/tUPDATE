package archive

import "testing"

func TestParseBackupFormat(t *testing.T) {
	cases := map[string]BackupFormat{
		"zip":    FormatZip,
		"ZIP":    FormatZip,
		"tar.xz": FormatTarXz,
		"tarxz":  FormatTarXz,
		" xz ":   FormatTarXz,
	}
	for in, want := range cases {
		got, ok := ParseBackupFormat(in)
		if !ok || got != want {
			t.Errorf("ParseBackupFormat(%q) = (%v, %v), want (%v, true)", in, got, ok, want)
		}
	}
	if _, ok := ParseBackupFormat("rar"); ok {
		t.Error("ParseBackupFormat(rar) should be ok=false")
	}
}

func TestParseCompressionLevel(t *testing.T) {
	cases := map[string]CompressionLevel{
		"min":     LevelMin,
		"default": LevelDefault,
		"MAX":     LevelMax,
		"fast":    LevelMin,
		"best":    LevelMax,
	}
	for in, want := range cases {
		got, ok := ParseCompressionLevel(in)
		if !ok || got != want {
			t.Errorf("ParseCompressionLevel(%q) = (%v, %v), want (%v, true)", in, got, ok, want)
		}
	}
	if _, ok := ParseCompressionLevel("ultra"); ok {
		t.Error("ParseCompressionLevel(ultra) should be ok=false")
	}
}

func TestFormatAndLevelString(t *testing.T) {
	if FormatTarXz.String() != "tar.xz" || FormatZip.String() != "zip" {
		t.Errorf("format strings wrong: %q %q", FormatTarXz, FormatZip)
	}
	if LevelMin.String() != "min" || LevelDefault.String() != "default" || LevelMax.String() != "max" {
		t.Errorf("level strings wrong: %q %q %q", LevelMin, LevelDefault, LevelMax)
	}
}
