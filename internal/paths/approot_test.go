package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppRoot_ReturnsDirOfRunningExecutable(t *testing.T) {
	got, err := AppRoot()
	if err != nil {
		t.Fatalf("AppRoot() error: %v", err)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat returned path: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("AppRoot() = %q, not a directory", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("AppRoot() = %q, expected absolute path", got)
	}
}
