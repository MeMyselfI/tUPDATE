package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// forceLocale pins the locale env vars for the duration of a test so that
// assertions against UI strings are deterministic regardless of the dev
// machine's environment.
func forceLocale(t *testing.T, lang string) {
	t.Helper()
	t.Setenv("LC_ALL", lang)
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
}

// writePropertiesWithSync writes a properties file with a custom sync.directories
// value and stop/start commands.
func writePropertiesWithSync(t *testing.T, dir, syncDirs, stopCmd, startCmd string) string {
	t.Helper()
	conf := filepath.Join(dir, "conf", "updater.properties")
	body := fmt.Sprintf(`download.url = http://127.0.0.1:1/x.zip
download.timeout.seconds = 30
proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy =
sync.directories = %s
service.stop.windows = %s
service.stop.darwin = %s
service.stop.linux = %s
service.start.windows = %s
service.start.darwin = %s
service.start.linux = %s
service.stop.timeout.seconds = 10
service.start.timeout.seconds = 10
backup.directory = backup
pgdump.path.windows = %s
pgdump.path.darwin  = %s
pgdump.path.linux   = %s
pgdump.host     = localhost
pgdump.user     = testuser
pgdump.password = testpw
pgdump.db       = testdb
`, syncDirs, stopCmd, stopCmd, stopCmd, startCmd, startCmd, startCmd,
		nonexistentPgDump, nonexistentPgDump, nonexistentPgDump)
	writeFile(t, conf, body)
	return conf
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
pgdump.path.windows = ` + nonexistentPgDump + `
pgdump.path.darwin  = ` + nonexistentPgDump + `
pgdump.path.linux   = ` + nonexistentPgDump + `
pgdump.host     = localhost
pgdump.user     = testuser
pgdump.password = testpw
pgdump.db       = testdb
`
	writeFile(t, conf, body)
	return conf
}

// nonexistentPgDump deterministically routes the DB-backup branch in tests
// to a failing exec without falling through to the host's PATH (which may or
// may not have a real pg_dump installed).
const nonexistentPgDump = "/tmp/this-pgdump-does-not-exist-tupdate-test"

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
	forceLocale(t, "en_US.UTF-8")
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, []string{"--version"}, "1.2.3")
	if code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "1.2.3") {
		t.Errorf("stdout = %q, missing version", stdout.String())
	}
}

func TestRunApp_DryRunShowsDiff(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
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
		"--lang", "en",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Diff:", "bin/", "www/", "etc/", "Total"} {
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

func TestRunApp_ShowAllFlowPrintsDetailsThenContinues(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{
		"bin/run.sh":     "v2",
		"www/index.html": "<html>v2</html>",
		"www/added.html": "<html>new</html>",
	})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")
	writeFile(t, filepath.Join(live, "www", "index.html"), "<html>v1</html>")
	writeFile(t, filepath.Join(live, "www", "stale.html"), "<html>old</html>")

	// Answers: 3-way=a (show), continue-after-details=y, backup=n, dbbackup=n, update=y
	stdin := strings.NewReader("a\ny\nn\nn\ny\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Detailed changes:",
		"[+] www/added.html",
		"[~] bin/run.sh",
		"[~] www/index.html",
		"[-] www/stale.html",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\n%s", want, out)
		}
	}
	// File should have been applied.
	if got, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh")); string(got) != "v2" {
		t.Errorf("bin/run.sh = %q, want v2", got)
	}
}

func TestRunApp_ThreeWayAbortStopsBeforeApply(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	stdin := strings.NewReader("n\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitUserAbort {
		t.Errorf("exit = %d, want exitUserAbort", code)
	}
	if got, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh")); string(got) != "v1" {
		t.Errorf("bin/run.sh modified despite abort: %q", got)
	}
}

func TestRunApp_AppliesUpdateWithNoPrompt(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
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
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
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
	if len(backups) != 1 || !strings.HasSuffix(backups[0].Name(), ".tar.xz") {
		t.Errorf("expected one *.tar.xz in backup/, got %v", backups)
	}
}

func TestRunApp_MissingZipReturnsConfigExit(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
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
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig=%d, stderr: %s", code, exitConfig, stderr.String())
	}
}

func TestRunApp_MissingConfigReturnsConfigExit(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	args := []string{
		"--config", filepath.Join(tmp, "missing.properties"),
		"--app-root", tmp,
		"--skip-service",
		"--dry-run",
	}
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig=%d", code, exitConfig)
	}
}

func TestRunApp_NoChangesExitsZeroWithoutPrompt(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
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
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Errorf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "No changes.") {
		t.Errorf("stderr missing 'No changes.': %s", stderr.String())
	}
}

func TestRunApp_SyncDirMissingOnBothSidesIsNoError(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
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
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
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
	forceLocale(t, "en_US.UTF-8")
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
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
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

func TestRunApp_LocalesUserVisibleStrings(t *testing.T) {
	cases := []struct {
		lang      string
		dryRunMsg string
	}{
		{"en_US.UTF-8", "Dry run finished"},
		{"de_DE.UTF-8", "Dry-Run beendet"},
		{"fr_FR.UTF-8", "Dry-run terminé"},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			forceLocale(t, c.lang)
			tmp := t.TempDir()
			live := filepath.Join(tmp, "live")
			zipPath := filepath.Join(tmp, "ref.zip")
			confPath := writeProperties(t, live)

			buildZip(t, zipPath, map[string]string{"bin/x": "1"})
			writeFile(t, filepath.Join(live, "bin", "x"), "0")

			args := []string{
				"--zip", zipPath,
				"--config", confPath,
				"--app-root", live,
				"--skip-service",
				"--dry-run",
			}
			var stdout, stderr bytes.Buffer
			code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
			if code != exitOK {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.Contains(stderr.String(), c.dryRunMsg) {
				t.Errorf("stderr missing %q\n%s", c.dryRunMsg, stderr.String())
			}
		})
	}
}

func TestRunApp_ServiceStopFailNoPromptAborts(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	// Stop command always fails; start command succeeds.
	confPath := writePropertiesWithSync(t, live, "bin", "false", "true")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--no-prompt",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitServiceStop {
		t.Errorf("exit = %d, want exitServiceStop=%d\nstderr: %s", code, exitServiceStop, stderr.String())
	}
	// Sync should not have been applied.
	got, _ := os.ReadFile(filepath.Join(live, "bin", "x"))
	if string(got) != "v1" {
		t.Errorf("bin/x modified despite stop fail: %q", got)
	}
}

func TestRunApp_ServiceStopFailContinueRunsWorkflow(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writePropertiesWithSync(t, live, "bin", "false", "true")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	// User answers:
	//   "Continue anyway?"              → y   (after stop fail)
	//   "Continue/abort/show?"          → y
	//   "Create backup?"                → n
	//   "DB-backup via pg_dump?"        → n
	//   "Apply update now?"             → y
	// Leading "y": the service run-state probe cannot classify the test stop
	// command ("true"/"false"), so tUPDATE asks whether to stop it anyway.
	stdin := strings.NewReader("y\ny\ny\nn\nn\ny\n")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	got, _ := os.ReadFile(filepath.Join(live, "bin", "x"))
	if string(got) != "v2" {
		t.Errorf("bin/x not applied: %q", got)
	}
	if !strings.Contains(stdout.String(), "Continue anyway?") {
		t.Errorf("expected 'Continue anyway?' prompt:\n%s", stdout.String())
	}
}

func TestRunApp_ServiceStopFailDeclineAborts(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writePropertiesWithSync(t, live, "bin", "false", "true")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	// "Continue anyway?" → n
	// Leading "y": the service run-state probe cannot classify the test stop
	// command ("true"/"false"), so tUPDATE asks whether to stop it anyway.
	stdin := strings.NewReader("y\nn\n")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitServiceStop {
		t.Errorf("exit = %d, want exitServiceStop", code)
	}
	got, _ := os.ReadFile(filepath.Join(live, "bin", "x"))
	if string(got) != "v1" {
		t.Errorf("bin/x changed despite user decline: %q", got)
	}
}

func TestRunApp_ServiceStartFailFinalDeclineReturnsStartExit(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	// Stop succeeds; start fails at the end.
	confPath := writePropertiesWithSync(t, live, "bin", "true", "false")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	// ContinueOrShow=y, Backup=n, DBBackup=n, Update=y, ContinueAnywayAfterStartFail=n.
	// Leading "y": the service run-state probe cannot classify the test stop
	// command ("true"/"false"), so tUPDATE asks whether to stop it anyway.
	stdin := strings.NewReader("y\ny\nn\nn\ny\nn\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitServiceStart {
		t.Errorf("exit = %d, want exitServiceStart=%d\nstderr: %s", code, exitServiceStart, stderr.String())
	}
	// Sync should have been applied (start failed AFTER apply).
	got, _ := os.ReadFile(filepath.Join(live, "bin", "x"))
	if string(got) != "v2" {
		t.Errorf("bin/x not applied: %q", got)
	}
}

func TestRunApp_UserDeclinesUpdate(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/new.sh": "V2\n"})
	writeFile(t, filepath.Join(live, "bin", "old.sh"), "OLD")

	// 3-way prompt → no (abort immediately).
	stdin := strings.NewReader("n\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
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

func TestRunApp_DBBackupPromptedIndependentlyAcceptsAndFailsGracefully(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	// 3-way=y, backup=n, dbbackup=y, update=y.
	// pgdump.path points at a nonexistent binary -> RunPgDump errors but the
	// workflow must continue and exit 0.
	stdin := strings.NewReader("y\nn\ny\ny\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "pg_dump") {
		t.Errorf("expected DB-Backup prompt to mention pg_dump:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "pg_dump failed:") {
		t.Errorf("expected pg_dump failure message in stderr:\n%s", stderr.String())
	}
	got, _ := os.ReadFile(filepath.Join(live, "bin", "run.sh"))
	if string(got) != "v2" {
		t.Errorf("update was not applied despite pg_dump failure: %q", got)
	}
}

func TestRunApp_DBBackupDeclinedSkipsPgDump(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	// 3-way=y, backup=n, dbbackup=n, update=y.
	stdin := strings.NewReader("y\nn\nn\ny\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Running pg_dump") {
		t.Errorf("pg_dump should not run when user declines, stderr:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "pg_dump failed") {
		t.Errorf("pg_dump should not run when user declines, stderr:\n%s", stderr.String())
	}
}

func TestRunApp_DBBackupAcceptedNoBinaryConfiguredShowsSkippedMessage(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	// Write conf WITHOUT any pgdump.path entries.
	confPath := filepath.Join(live, "conf", "updater.properties")
	body := `download.url = http://127.0.0.1:1/x.zip
download.timeout.seconds = 30
proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy =
sync.directories = bin
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
	writeFile(t, confPath, body)

	// Wipe PATH so exec.LookPath cannot find a real pg_dump and we hit the
	// "not found" branch deterministically.
	t.Setenv("PATH", "")

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	// 3-way=y, backup=n, dbbackup=y, update=y.
	stdin := strings.NewReader("y\nn\ny\ny\n")
	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("expected 'pg_dump not found' info in stderr:\n%s", stderr.String())
	}
}

func TestRunApp_NoBackupSkipsZipPromptAndZip(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--no-prompt",
		"--no-files-backup",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	backupDir := filepath.Join(live, "backup")
	entries, _ := os.ReadDir(backupDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".zip") {
			t.Errorf("--no-files-backup should skip ZIP, found: %s", e.Name())
		}
	}
}

func TestRunApp_NoDBBackupSkipsPgDump(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)

	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--skip-service",
		"--no-prompt",
		"--no-db-backup",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Running pg_dump") {
		t.Errorf("--no-db-backup should skip pg_dump, stderr:\n%s", stderr.String())
	}
}

func TestRunApp_IgnoreServiceErrorsContinuesPastStopFail(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	// stop fails, start succeeds.
	confPath := writePropertiesWithSync(t, live, "bin", "false", "true")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--no-prompt",
		"--no-files-backup",
		"--no-db-backup",
		"--ignore-service-errors",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Service stop error:") {
		t.Errorf("expected stop error still printed:\n%s", stderr.String())
	}
	got, _ := os.ReadFile(filepath.Join(live, "bin", "x"))
	if string(got) != "v2" {
		t.Errorf("update not applied despite --ignore-service-errors: %q", got)
	}
}

func TestRunApp_IgnoreServiceErrorsContinuesPastStartFail(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	// stop succeeds, start fails.
	confPath := writePropertiesWithSync(t, live, "bin", "true", "false")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--no-prompt",
		"--no-files-backup",
		"--no-db-backup",
		"--ignore-service-errors",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Service start error:") {
		t.Errorf("expected start error still printed:\n%s", stderr.String())
	}
}

func TestRunApp_NoPromptWithoutIgnoreStillAbortsOnStopFail(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writePropertiesWithSync(t, live, "bin", "false", "true")
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	args := []string{
		"--zip", zipPath,
		"--config", confPath,
		"--app-root", live,
		"--no-prompt",
	}
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, args, "test")
	if code != exitServiceStop {
		t.Errorf("exit = %d, want exitServiceStop", code)
	}
}

func TestRunApp_HelpGoesToStdoutAndReturnsOK(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard, []string{"--help"}, "test")
	if code != exitOK {
		t.Errorf("exit = %d, want exitOK", code)
	}
	if !strings.Contains(stdout.String(), "USAGE") {
		t.Errorf("--help output not on stdout; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "USAGE") {
		t.Errorf("--help leaked to stderr:\n%s", stderr.String())
	}
}

func TestRunApp_JsonRequiresNoPrompt(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	var stdout, stderr, emit bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, &emit,
		[]string{"--json"}, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig", code)
	}
	if !strings.Contains(stderr.String(), "--json requires --no-prompt") {
		t.Errorf("expected --json error in stderr:\n%s", stderr.String())
	}
}

func TestRunApp_LangOverrideStrict(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	var stdout, stderr, emit bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, &emit,
		[]string{"--lang=xx", "--version"}, "test")
	if code != exitConfig {
		t.Errorf("exit = %d, want exitConfig for invalid lang", code)
	}
	if !strings.Contains(stderr.String(), "--lang") {
		t.Errorf("expected --lang error in stderr:\n%s", stderr.String())
	}
}

func TestRunApp_LangOverrideAcceptsDeEnFr(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	for _, lang := range []string{"de", "en", "fr"} {
		var stdout, stderr, emit bytes.Buffer
		code := runApp(strings.NewReader(""), &stdout, &stderr, &emit,
			[]string{"--lang", lang, "--version"}, "test")
		if code != exitOK {
			t.Errorf("--lang=%s: exit = %d, want exitOK\nstderr: %s", lang, code, stderr.String())
		}
	}
}

func TestRunApp_JsonModeProducesNDJSONOnEmitWriter(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)
	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	var stdout, stderr, emit bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, &emit,
		[]string{
			"--zip", zipPath,
			"--config", confPath,
			"--app-root", live,
			"--skip-service",
			"--no-prompt",
			"--no-files-backup",
			"--no-db-backup",
			"--json",
		}, "test")
	if code != exitOK {
		t.Fatalf("exit = %d\nstderr: %s\nemit: %s", code, stderr.String(), emit.String())
	}
	// stdout / stderr in JSON mode should be empty: humans are suppressed.
	if stdout.Len() != 0 {
		t.Errorf("--json: stdout not empty:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("--json: stderr not empty:\n%s", stderr.String())
	}

	lines := strings.Split(strings.TrimRight(emit.String(), "\n"), "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		seen[m["event"].(string)] = true
	}
	for _, want := range []string{"diff", "apply_ok", "exit"} {
		if !seen[want] {
			t.Errorf("expected %q event in JSON stream, got events: %v", want, seen)
		}
	}
}

func TestRunApp_DryRunChecksPassWhenLocalZipAndWritableBackup(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)
	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard,
		[]string{
			"--zip", zipPath,
			"--config", confPath,
			"--app-root", live,
			"--skip-service",
			"--dry-run",
			"--no-db-backup", // skip pg_dump check (binary doesn't exist in tests)
		}, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Check backup_dir_writable: OK") {
		t.Errorf("expected writable-OK in stderr:\n%s", stderr.String())
	}
}

func TestRunApp_DryRunChecksFailExit9WhenBackupDirUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("can't make dir unwritable as root")
	}
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	confPath := writeProperties(t, live)
	buildZip(t, zipPath, map[string]string{"bin/run.sh": "v2"})
	writeFile(t, filepath.Join(live, "bin", "run.sh"), "v1")

	// Force backup-dir creation to fail by creating a file at that path.
	if err := os.WriteFile(filepath.Join(live, "backup"), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runApp(strings.NewReader(""), &stdout, &stderr, io.Discard,
		[]string{
			"--zip", zipPath,
			"--config", confPath,
			"--app-root", live,
			"--skip-service",
			"--dry-run",
			"--no-db-backup",
		}, "test")
	if code != exitDryRunCheck {
		t.Errorf("exit = %d, want exitDryRunCheck (9)", code)
	}
	if !strings.Contains(stderr.String(), "FAIL") {
		t.Errorf("expected FAIL marker in stderr:\n%s", stderr.String())
	}
}

func TestProbeServiceBinary_FoundAndNotFound(t *testing.T) {
	// On any unix-y CI box /bin/sh exists; on Windows we can't rely on it.
	if ok, _ := probeServiceBinary("/bin/sh -c true"); !ok {
		t.Errorf("expected /bin/sh to resolve")
	}
	ok, detail := probeServiceBinary("definitely-not-a-binary-tupdate-xyz stop foo")
	if ok {
		t.Errorf("expected nonexistent binary to fail, detail=%q", detail)
	}
	if ok, _ := probeServiceBinary(""); ok {
		t.Errorf("empty command should fail")
	}
}

func TestProbePgdumpString_PrefersConfThenEnv(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	if ok, _ := probePgdumpString("appdb", "PGDATABASE", "pgdump.db"); !ok {
		t.Error("conf value should win")
	}
	t.Setenv("PGDATABASE", "envdb")
	ok, detail := probePgdumpString("", "PGDATABASE", "pgdump.db")
	if !ok || !strings.Contains(detail, "PGDATABASE env") {
		t.Errorf("env should win when conf empty, ok=%v detail=%q", ok, detail)
	}
	t.Setenv("PGDATABASE", "")
	ok, detail = probePgdumpString("", "PGDATABASE", "pgdump.db")
	if ok {
		t.Errorf("expected fail when both empty, detail=%q", detail)
	}
}

func TestProbePgdumpPassword_NeverLeaksValue(t *testing.T) {
	t.Setenv("PGPASSWORD", "")
	t.Setenv("HOME", t.TempDir()) // no .pgpass present
	if ok, _ := probePgdumpPassword(""); ok {
		t.Error("expected fail when nothing is set")
	}
	ok, detail := probePgdumpPassword("super-s3cret-password")
	if !ok {
		t.Errorf("conf value should satisfy check")
	}
	if strings.Contains(detail, "super-s3cret-password") {
		t.Errorf("detail leaked the password: %q", detail)
	}
	t.Setenv("PGPASSWORD", "env-s3cret-password")
	ok, detail = probePgdumpPassword("")
	if !ok || strings.Contains(detail, "env-s3cret-password") {
		t.Errorf("env path leaked password, ok=%v detail=%q", ok, detail)
	}
}

func TestProbePgdumpPassword_PgpassFileSatisfies(t *testing.T) {
	t.Setenv("PGPASSWORD", "")
	tmpHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpHome, ".pgpass"),
		[]byte("localhost:5432:db:user:pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)
	ok, detail := probePgdumpPassword("")
	if !ok {
		t.Errorf("~/.pgpass should satisfy, detail=%q", detail)
	}
	if !strings.Contains(detail, ".pgpass") {
		t.Errorf("expected detail to mention .pgpass, got %q", detail)
	}
}

// tUPDATE must not start a service it never stopped. Here the stop command
// fails and the operator continues anyway: the update goes through, but the
// start command must stay untouched.
func TestRunApp_StopFailedMeansNoStartAtTheEnd(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	marker := filepath.Join(tmp, "started.marker")

	confPath := writePropertiesWithSync(t, live, "bin", "false", "touch "+marker)
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	// stop-anyway=y, continue-after-stop-fail=y, 3-way=y, backup=n, db=n, update=y
	stdin := strings.NewReader("y\ny\ny\nn\nn\ny\n")
	args := []string{"--zip", zipPath, "--config", confPath, "--app-root", live}

	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if got, _ := os.ReadFile(filepath.Join(live, "bin", "x")); string(got) != "v2" {
		t.Errorf("bin/x not applied: %q", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("start command ran although tUPDATE never stopped the service")
	}
	if !strings.Contains(stderr.String(), "start skipped") {
		t.Errorf("expected a 'start skipped' notice:\n%s", stderr.String())
	}
}

// --force-start-service overrides that rule.
func TestRunApp_ForceStartServiceStartsWithoutAPriorStop(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	marker := filepath.Join(tmp, "started.marker")

	confPath := writePropertiesWithSync(t, live, "bin", "false", "touch "+marker)
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	stdin := strings.NewReader("y\ny\ny\nn\nn\ny\n")
	args := []string{
		"--zip", zipPath, "--config", confPath, "--app-root", live,
		"--force-start-service",
	}

	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("start command did not run despite --force-start-service: %v", err)
	}
}

// Declining the "stop anyway?" question on an inconclusive status probe means
// the service is neither stopped nor started, while the update still applies.
func TestRunApp_DeclineStopOnUnknownStateLeavesServiceAlone(t *testing.T) {
	forceLocale(t, "en_US.UTF-8")
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live")
	zipPath := filepath.Join(tmp, "ref.zip")
	stopMarker := filepath.Join(tmp, "stopped.marker")
	startMarker := filepath.Join(tmp, "started.marker")

	confPath := writePropertiesWithSync(t, live, "bin", "touch "+stopMarker, "touch "+startMarker)
	buildZip(t, zipPath, map[string]string{"bin/x": "v2"})
	writeFile(t, filepath.Join(live, "bin", "x"), "v1")

	// stop-anyway=n, 3-way=y, backup=n, db=n, update=y
	stdin := strings.NewReader("n\ny\nn\nn\ny\n")
	args := []string{"--zip", zipPath, "--config", confPath, "--app-root", live}

	var stdout, stderr bytes.Buffer
	code := runApp(stdin, &stdout, &stderr, io.Discard, args, "test")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if got, _ := os.ReadFile(filepath.Join(live, "bin", "x")); string(got) != "v2" {
		t.Errorf("bin/x not applied: %q", got)
	}
	if _, err := os.Stat(stopMarker); err == nil {
		t.Error("stop command ran although the operator declined")
	}
	if _, err := os.Stat(startMarker); err == nil {
		t.Error("start command ran although the service was never stopped")
	}
}
