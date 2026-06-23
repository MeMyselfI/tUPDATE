package main

import (
	"io"
	"strings"
	"testing"

	"updater/internal/archive"
	"updater/internal/i18n"
	"updater/internal/prompt"
)

func TestResolveBackupOptions_FlagOverride(t *testing.T) {
	f := &flagSet{backupFormat: "zip", backupCompression: "max"}
	opts, err := resolveBackupOptions(f, prompt.Always{Answer: true}, i18n.Get(i18n.LangEN))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format != archive.FormatZip || opts.Level != archive.LevelMax {
		t.Errorf("got %v/%v, want zip/max", opts.Format, opts.Level)
	}
}

func TestResolveBackupOptions_DefaultsUnderNoPrompt(t *testing.T) {
	opts, err := resolveBackupOptions(&flagSet{}, prompt.Always{Answer: true}, i18n.Get(i18n.LangEN))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format != archive.FormatTarXz || opts.Level != archive.LevelDefault {
		t.Errorf("got %v/%v, want tar.xz/default", opts.Format, opts.Level)
	}
}

func TestResolveBackupOptions_InvalidFlag(t *testing.T) {
	if _, err := resolveBackupOptions(&flagSet{backupFormat: "rar"}, prompt.Always{Answer: true}, i18n.Get(i18n.LangEN)); err == nil {
		t.Error("expected error for invalid --backup-format")
	}
	if _, err := resolveBackupOptions(&flagSet{backupCompression: "ultra"}, prompt.Always{Answer: true}, i18n.Get(i18n.LangEN)); err == nil {
		t.Error("expected error for invalid --backup-compression")
	}
}

func TestResolveBackupOptions_InteractiveChoose(t *testing.T) {
	// Menu order: format [tar.xz, zip] -> "2"=zip; level [min, default, max] -> "3"=max.
	p := &prompt.Stdin{In: strings.NewReader("2\n3\n"), Out: io.Discard, MaxAttempts: 3}
	opts, err := resolveBackupOptions(&flagSet{}, p, i18n.Get(i18n.LangEN))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format != archive.FormatZip || opts.Level != archive.LevelMax {
		t.Errorf("got %v/%v, want zip/max", opts.Format, opts.Level)
	}
}
