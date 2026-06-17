package config

import (
	"strings"
	"testing"
)

const validConf = `
download.url = https://example.com/app.zip
download.timeout.seconds = 300

proxy.url =
proxy.user =
proxy.password =
proxy.no_proxy = localhost,127.0.0.1

sync.directories = bin, www, etc , libs

service.stop.windows  = net stop xApp
service.stop.darwin   = launchctl stop org.example.app
service.stop.linux    = systemctl stop xapp
service.start.windows = net start xApp
service.start.darwin  = launchctl start org.example.app
service.start.linux   = systemctl start xapp
service.stop.timeout.seconds  = 60
service.start.timeout.seconds = 60

backup.directory = backup
`

func loadFromString(t *testing.T, content string) (*Config, error) {
	t.Helper()
	props, err := Parse(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	return FromMap(props)
}

func TestFromMap_Valid(t *testing.T) {
	cfg, err := loadFromString(t, validConf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DownloadURL != "https://example.com/app.zip" {
		t.Errorf("DownloadURL = %q", cfg.DownloadURL)
	}
	if cfg.DownloadTimeoutSecs != 300 {
		t.Errorf("DownloadTimeoutSecs = %d", cfg.DownloadTimeoutSecs)
	}
	want := []string{"bin", "www", "etc", "libs"}
	if len(cfg.SyncDirectories) != len(want) {
		t.Fatalf("SyncDirectories len = %d, want %d", len(cfg.SyncDirectories), len(want))
	}
	for i, d := range want {
		if cfg.SyncDirectories[i] != d {
			t.Errorf("SyncDirectories[%d] = %q, want %q", i, cfg.SyncDirectories[i], d)
		}
	}
	if cfg.ServiceStop["windows"] != "net stop xApp" {
		t.Errorf("ServiceStop[windows] = %q", cfg.ServiceStop["windows"])
	}
	if cfg.ServiceStart["linux"] != "systemctl start xapp" {
		t.Errorf("ServiceStart[linux] = %q", cfg.ServiceStart["linux"])
	}
	if cfg.ServiceStopTimeoutSecs != 60 {
		t.Errorf("ServiceStopTimeoutSecs = %d", cfg.ServiceStopTimeoutSecs)
	}
	if cfg.BackupDirectory != "backup" {
		t.Errorf("BackupDirectory = %q", cfg.BackupDirectory)
	}
	if cfg.ProxyURL != "" {
		t.Errorf("ProxyURL = %q", cfg.ProxyURL)
	}
	if cfg.ProxyNoProxy != "localhost,127.0.0.1" {
		t.Errorf("ProxyNoProxy = %q", cfg.ProxyNoProxy)
	}
}

func TestFromMap_MissingRequiredKey(t *testing.T) {
	conf := strings.Replace(validConf, "download.url = https://example.com/app.zip", "", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for missing download.url")
	}
	if !strings.Contains(err.Error(), "download.url") {
		t.Errorf("expected error to mention download.url: %v", err)
	}
}

func TestFromMap_MissingServiceCommand(t *testing.T) {
	conf := strings.Replace(validConf, "service.stop.linux    = systemctl stop xapp", "", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for missing service.stop.linux")
	}
	if !strings.Contains(err.Error(), "service.stop.linux") {
		t.Errorf("expected error to mention service.stop.linux: %v", err)
	}
}

func TestFromMap_InvalidIntTimeout(t *testing.T) {
	conf := strings.Replace(validConf, "download.timeout.seconds = 300", "download.timeout.seconds = abc", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for invalid int")
	}
	if !strings.Contains(err.Error(), "download.timeout.seconds") {
		t.Errorf("expected error to mention key: %v", err)
	}
}

func TestFromMap_NegativeTimeout(t *testing.T) {
	conf := strings.Replace(validConf, "download.timeout.seconds = 300", "download.timeout.seconds = -1", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func TestFromMap_EmptySyncDirectories(t *testing.T) {
	conf := strings.Replace(validConf, "sync.directories = bin, www, etc , libs", "sync.directories =", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for empty sync.directories")
	}
}

func TestFromMap_SyncDirAbsolutePathRejected(t *testing.T) {
	conf := strings.Replace(validConf, "sync.directories = bin, www, etc , libs", "sync.directories = bin, /etc", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for absolute path in sync.directories")
	}
}

func TestFromMap_SyncDirParentTraversalRejected(t *testing.T) {
	conf := strings.Replace(validConf, "sync.directories = bin, www, etc , libs", "sync.directories = bin, ../etc", 1)
	_, err := loadFromString(t, conf)
	if err == nil {
		t.Fatal("expected error for parent traversal in sync.directories")
	}
}

func TestFromMap_UnknownKeyTolerated(t *testing.T) {
	conf := validConf + "\nfuture.unknown.key = something\n"
	if _, err := loadFromString(t, conf); err != nil {
		t.Errorf("unknown key should not error, got: %v", err)
	}
}

func TestFromMap_ProxyValuesPropagated(t *testing.T) {
	conf := strings.Replace(validConf, "proxy.url =", "proxy.url = http://proxy.example.com:8080", 1)
	conf = strings.Replace(conf, "proxy.user =", "proxy.user = alice", 1)
	conf = strings.Replace(conf, "proxy.password =", "proxy.password = s3cret", 1)
	cfg, err := loadFromString(t, conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ProxyURL != "http://proxy.example.com:8080" {
		t.Errorf("ProxyURL = %q", cfg.ProxyURL)
	}
	if cfg.ProxyUser != "alice" || cfg.ProxyPassword != "s3cret" {
		t.Errorf("proxy creds wrong: user=%q password=%q", cfg.ProxyUser, cfg.ProxyPassword)
	}
}

func TestLoad_FromSampleFile(t *testing.T) {
	cfg, err := Load("../../conf/updater.properties")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.DownloadURL == "" {
		t.Error("DownloadURL empty")
	}
	if len(cfg.SyncDirectories) == 0 {
		t.Error("SyncDirectories empty")
	}
	for _, os := range []string{"windows", "darwin", "linux"} {
		if cfg.ServiceStop[os] == "" {
			t.Errorf("ServiceStop[%s] empty", os)
		}
		if cfg.ServiceStart[os] == "" {
			t.Errorf("ServiceStart[%s] empty", os)
		}
	}
}

func TestFromMap_PgdumpOptional(t *testing.T) {
	cfg, err := loadFromString(t, validConf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, os := range []string{"windows", "darwin", "linux"} {
		if cfg.PgdumpBinary(os) != "" {
			t.Errorf("PgdumpBinary(%s) should be empty, got %q", os, cfg.PgdumpBinary(os))
		}
	}
	if len(cfg.PgdumpArgs) != 0 {
		t.Errorf("PgdumpArgs should be empty, got %v", cfg.PgdumpArgs)
	}
}

func TestFromMap_PgdumpConfigured(t *testing.T) {
	conf := validConf + `
pgdump.path.windows = C:\pg\pg_dump.exe
pgdump.path.darwin  = /usr/local/bin/pg_dump
pgdump.path.linux   = /usr/bin/pg_dump
pgdump.args = -h localhost -p 5432 -U postgres mydb
`
	cfg, err := loadFromString(t, conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.PgdumpBinary("linux"); got != "/usr/bin/pg_dump" {
		t.Errorf("PgdumpBinary(linux) = %q", got)
	}
	if got := cfg.PgdumpBinary("darwin"); got != "/usr/local/bin/pg_dump" {
		t.Errorf("PgdumpBinary(darwin) = %q", got)
	}
	if got := cfg.PgdumpBinary("windows"); got != `C:\pg\pg_dump.exe` {
		t.Errorf("PgdumpBinary(windows) = %q", got)
	}
	want := []string{"-h", "localhost", "-p", "5432", "-U", "postgres", "mydb"}
	if len(cfg.PgdumpArgs) != len(want) {
		t.Fatalf("PgdumpArgs len = %d, want %d", len(cfg.PgdumpArgs), len(want))
	}
	for i, a := range want {
		if cfg.PgdumpArgs[i] != a {
			t.Errorf("PgdumpArgs[%d] = %q, want %q", i, cfg.PgdumpArgs[i], a)
		}
	}
}

func TestServiceCommandForOS(t *testing.T) {
	cfg, err := loadFromString(t, validConf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.StopCommand("linux"); got != "systemctl stop xapp" {
		t.Errorf("StopCommand(linux) = %q", got)
	}
	if got := cfg.StartCommand("darwin"); got != "launchctl start org.example.app" {
		t.Errorf("StartCommand(darwin) = %q", got)
	}
	if got := cfg.StopCommand("plan9"); got != "" {
		t.Errorf("StopCommand(plan9) = %q, want empty", got)
	}
}
