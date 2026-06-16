package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the typed configuration loaded from updater.properties.
type Config struct {
	DownloadURL         string
	DownloadTimeoutSecs int

	ProxyURL      string
	ProxyUser     string
	ProxyPassword string
	ProxyNoProxy  string

	SyncDirectories []string

	ServiceStop             map[string]string
	ServiceStart            map[string]string
	ServiceStopTimeoutSecs  int
	ServiceStartTimeoutSecs int

	BackupDirectory string
}

// supportedOS lists the runtime.GOOS values for which service commands must be configured.
var supportedOS = []string{"windows", "darwin", "linux"}

// Load reads and validates a properties file.
func Load(path string) (*Config, error) {
	props, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	return FromMap(props)
}

// FromMap builds and validates a Config from an already-parsed properties map.
func FromMap(props map[string]string) (*Config, error) {
	cfg := &Config{
		ServiceStop:  make(map[string]string, len(supportedOS)),
		ServiceStart: make(map[string]string, len(supportedOS)),
	}

	var err error

	cfg.DownloadURL, err = requiredString(props, "download.url")
	if err != nil {
		return nil, err
	}
	cfg.DownloadTimeoutSecs, err = requiredPositiveInt(props, "download.timeout.seconds")
	if err != nil {
		return nil, err
	}

	cfg.ProxyURL = props["proxy.url"]
	cfg.ProxyUser = props["proxy.user"]
	cfg.ProxyPassword = props["proxy.password"]
	cfg.ProxyNoProxy = props["proxy.no_proxy"]

	cfg.SyncDirectories, err = parseSyncDirectories(props["sync.directories"])
	if err != nil {
		return nil, err
	}

	for _, os := range supportedOS {
		stopKey := "service.stop." + os
		startKey := "service.start." + os
		stopVal, err := requiredString(props, stopKey)
		if err != nil {
			return nil, err
		}
		startVal, err := requiredString(props, startKey)
		if err != nil {
			return nil, err
		}
		cfg.ServiceStop[os] = stopVal
		cfg.ServiceStart[os] = startVal
	}

	cfg.ServiceStopTimeoutSecs, err = requiredPositiveInt(props, "service.stop.timeout.seconds")
	if err != nil {
		return nil, err
	}
	cfg.ServiceStartTimeoutSecs, err = requiredPositiveInt(props, "service.start.timeout.seconds")
	if err != nil {
		return nil, err
	}

	cfg.BackupDirectory, err = requiredString(props, "backup.directory")
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// StopCommand returns the stop command for the given GOOS, or "" if unconfigured.
func (c *Config) StopCommand(goos string) string {
	return c.ServiceStop[goos]
}

// StartCommand returns the start command for the given GOOS, or "" if unconfigured.
func (c *Config) StartCommand(goos string) string {
	return c.ServiceStart[goos]
}

func requiredString(props map[string]string, key string) (string, error) {
	v, ok := props[key]
	if !ok || strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("config: required key %q is missing or empty", key)
	}
	return v, nil
}

func requiredPositiveInt(props map[string]string, key string) (int, error) {
	v, ok := props[key]
	if !ok || strings.TrimSpace(v) == "" {
		return 0, fmt.Errorf("config: required key %q is missing or empty", key)
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("config: key %q: not an integer: %q", key, v)
	}
	if n <= 0 {
		return 0, fmt.Errorf("config: key %q: must be > 0, got %d", key, n)
	}
	return n, nil
}

func parseSyncDirectories(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("config: required key %q is missing or empty", "sync.directories")
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if filepath.IsAbs(p) {
			return nil, fmt.Errorf("config: sync.directories: absolute path not allowed: %q", p)
		}
		clean := filepath.Clean(p)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("config: sync.directories: parent traversal not allowed: %q", p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("config: sync.directories: no valid entries")
	}
	return out, nil
}
