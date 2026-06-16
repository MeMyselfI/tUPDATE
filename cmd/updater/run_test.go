package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProperties(t *testing.T, dir string) string {
	t.Helper()
	conf := filepath.Join(dir, "conf", "updater.properties")
	body := `download.url = http://127.0.0.1:1/x.zip
download.timeout.seconds = 30
proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy =
sync.directories = bin, www, etc
service.stop.windows = exit 0
service.stop.darwin = true
service.stop.linux = true
service.start.windows = exit 0
service.start.darwin = true
service.start.linux = true
service.stop.timeout.seconds = 10
service.start.timeout.seconds = 10
backup.directory = backup
`
	writeFile(t, conf, body)
	return conf
}

func buildZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()
	for name, body := range entries {
		fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
		fh.SetMode(0o644)
		wr, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatalf("create header %s: %v", name, err)
		}
		if _, err := wr.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunApp_VersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, []string{"--version"}, "1.2.3")
	if code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "1.2.3") {
		t.Errorf("stdout = %q, missing version", stdout.String())
	}
}

func TestRunApp_DryRunShowsDiff(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	// Reference (in ZIP): bin/run.sh modified, etc/new.cfg added; no www
	buildZip(t, zipPath, map[string]string{
		"bin/run.sh":  "V2-run\n",
		"etc/new.cfg": "added=1\n",
	})

	// Live tree: bin/run.sh has V1, www/old.html exists, etc empty
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "V1-run\n")
	writeFile(t, filepath.Join(live, "www", "old.html"), "old")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--dry-run",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Diff:", "bin/", "www/", "etc/", "Gesamt"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\n%s", want, out)
		}
	}
	// Files in live must remain unchanged.
	got, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh"))
	if string(got) != "V1-run\n" {
		t.Errorf("live bin/run.sh modified during dry-run: %q", got)
	}
	if _, err := os.Stat(filepath.Join(live, "etc", "new.cfg")); err == nil {
		t.Error("etc/new.cfg created during dry-run")
	}
}

func TestRunApp_AppliesUpdateWithNoPrompt(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{
		"bin/run.sh":  "V2-run\n",
		"etc/new.cfg": "added=1\n",
	})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "V1-run\n")
	writeFile(t, filepath.Join(live, "www", "stale.html"), "stale")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--no-prompt",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}

	gotRun, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh"))
	if string(gotRun) != "V2-run\n" {
		t.Errorf("bin/run.sh = %q, want V2", gotRun)
	}
	gotCfg, _ := os.ReadFile(filepath.Join(live, "etc", "new.cfg"))
	if string(gotCfg) != "added=1\n" {
		t.Errorf("etc/new.cfg = %q", gotCfg)
	}
	if _, err := os.Stat(filepath.Join(live, "www", "stale.html")); !os.IsNotExist(err) {
		t.Errorf("www/stale.html should have been removed, err=%v", err)
	}

	// --no-prompt with default true should produce a backup.
	backups, err := os.ReadDir(filepath.Join(live, "backup"))
	if err != nil {
		t.Fatalf("backup dir missing: %v", err)
	}
	if len(backups) != 1 || !strings.HasSuffix(backups[0].Name(), ".zip") {
		t.Errorf("expected one *.zip in backup/, got %v", backups)
	}
}

func TestRunApp_MissingZipReturnsConfigExit(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	confPath := writeProperties(t, live)

	args := []string{
		"--zip", filepath.Join(tmp, "does-not-exist.zip"),
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--dry-run",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig=%d, stderr: %s", code, exitConfig, stderr.String())
	}
}

func TestRunApp_MissingConfigReturnsConfigExit(t *testing.T) {
	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	args := []string{
		"--config", filepath.Join(tmp, "missing.properties"),
		"--app-root", tmp,
		"--skip-service",
		"--dry-run",
	}
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig=%d", code, exitConfig)
	}
}

func TestRunApp_NoChangesExitsZeroWithoutPrompt(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	// Identical content in ZIP and live → no changes.
	buildZip(t, zipPath, map[string]string{
		"bin/run.sh": "same\n",
	})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "same\n")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitOK {
		t.Errorf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Keine Änderungen") {
		t.Errorf("stderr missing 'Keine Änderungen': %s", stderr.String())
	}
}

func TestRunApp_SyncDirMissingOnBothSidesIsNoError(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")

	// Write a config that lists 'lib' (which exists in neither ZIP nor live tree)
	// alongside an actually-present dir.
	conf := filepath.Join(live, "conf", "updater.properties")
	writeFile(t, conf, `download.url = http://127.0.0.1:1/x.zip
download.timeout.seconds = 30
proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy =
sync.directories = bin, lib, libs, ghost
service.stop.windows = exit 0
service.stop.darwin = true
service.stop.linux = true
service.start.windows = exit 0
service.start.darwin = true
service.start.linux = true
service.stop.timeout.seconds = 10
service.start.timeout.seconds = 10
backup.directory = backup
`)

	// Reference: only bin/ entry; libs/lib/ghost intentionally not in ZIP.
	buildZip(t, zipPath, map[string]string{
		"bin/run.sh": "V2\n",
	})
	// Live: bin/run.sh V1 only; libs/lib/ghost absent.
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "V1\n")

	args := []string{
		"--zip", zipPath,
		"--config", conf,
		"--app-root", live,
		"--skip-service",
		"--no-prompt",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}

	for _, dir := range []string{"lib/", "libs/", "ghost/"} {
		if !strings.Contains(stdout.String(), dir) {
			t.Errorf("report should list %q even when missing: %s", dir, stdout.String())
		}
	}
	got, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh"))
	if string(got) != "V2\n" {
		t.Errorf("bin/run.sh not updated, got %q", got)
	}
	for _, dir := range []string{"lib", "libs", "ghost"} {
		if _, err := os.Stat(filepath.Join(live, dir)); err == nil {
			t.Errorf("phantom dir %s should not have been created", dir)
		}
	}
}

func TestRunApp_SyncDirOnlyInZipGetsCreated(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)
	// writeProperties uses sync.directories = bin, www, etc — fine.

	// ZIP introduces a new file under www that didn't exist live.
	buildZip(t, zipPath, map[string]string{
		"www/fresh.html": "<html>new</html>",
	})
	// Live has bin/x but no www/ at all.
	writeFile(t, filepath.Join(live, "bin", "x"), "x")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--no-prompt",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(live, "www", "fresh.html"))
	if err != nil {
		t.Fatalf("www/fresh.html should have been created: %v", err)
	}
	if string(got) != "<html>new</html>" {
		t.Errorf("content = %q", got)
	}
}

func TestRunApp_UserDeclinesUpdate(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/new.sh": "V2\n"})
	writeFile(t, filepath.Join(live, "bin", "old.sh"), "OLD")

	// First prompt: backup → no. Second prompt: update → no.
	stdin := strings.NewReader("n\nn\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, args, "test")
	if code != exitUserAbort {
		t.Errorf("exit = %d, want exitUserAbort=%d", code, exitUserAbort)
	}
	// File should not have been removed
	if _, err := os.Stat(filepath.Join(live, "bin", "old.sh")); err != nil {
		t.Errorf("bin/old.sh removed despite user declining: %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "bin", "new.sh")); err == nil {
		t.Errorf("bin/new.sh created despite user declining")
	}
}
