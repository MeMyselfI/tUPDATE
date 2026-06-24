package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseServiceTarget(t *testing.T) {
	tests := []struct {
		name        string
		stopCmd     string
		wantManager string
		wantID      string
	}{
		{"launchctl", "launchctl stop tosce", "launchctl", "tosce"},
		{"systemctl", "systemctl stop tosce", "systemctl", "tosce"},
		{"systemctl with flag", "systemctl --user stop tosce", "systemctl", "tosce"},
		{"net", "net stop tOSCE-Server", "net", "tOSCE-Server"},
		{"net quoted", `net stop "tOSCE-Server"`, "net", "tOSCE-Server"},
		{"sc", "sc stop tosce", "sc", "tosce"},
		{"absolute path binary", "/bin/launchctl stop tosce", "launchctl", "tosce"},
		{"windows .exe binary", `C:\Windows\net.exe stop tosce`, "net", "tosce"},
		{"unknown manager", "true", "", ""},
		{"unknown manager with id", "myctl stop tosce", "", ""},
		{"empty", "", "", ""},
		{"only manager no id", "systemctl", "", ""},
		{"trailing flag is not an id", "systemctl stop --now", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotM, gotID := parseServiceTarget(tc.stopCmd)
			if gotM != tc.wantManager || gotID != tc.wantID {
				t.Errorf("parseServiceTarget(%q) = (%q, %q), want (%q, %q)",
					tc.stopCmd, gotM, gotID, tc.wantManager, tc.wantID)
			}
		})
	}
}

func TestInterpretLaunchctl(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		runErr  error
		wantOK  bool
		wantSub string
	}{
		{"loaded with PID", "{\n\t\"PID\" = 4321;\n}", nil, true, "running"},
		{"loaded no PID", "{\n\t\"LastExitStatus\" = 0;\n}", nil, false, "not running"},
		{"not loaded (exit err)", "Could not find service", errors.New("exit 113"), false, "not loaded"},
		{"empty unparseable", "", nil, true, "inconclusive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := interpretLaunchctl(tc.out, tc.runErr)
			if ok != tc.wantOK || !strings.Contains(detail, tc.wantSub) {
				t.Errorf("interpretLaunchctl = (%v, %q), want ok=%v containing %q",
					ok, detail, tc.wantOK, tc.wantSub)
			}
		})
	}
}

func TestInterpretSystemctl(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		wantOK  bool
		wantSub string
	}{
		{"active", "active", true, "running"},
		{"activating", "activating", true, "running"},
		{"inactive", "inactive", false, "not running"},
		{"failed", "failed", false, "not running"},
		{"unknown state", "unknown", true, "inconclusive"},
		{"empty", "", true, "inconclusive"},
		{"trailing newline", "active\n", true, "running"},
		{"multi-unit first wins", "inactive\nactive", false, "not running"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := interpretSystemctl(tc.out)
			if ok != tc.wantOK || !strings.Contains(detail, tc.wantSub) {
				t.Errorf("interpretSystemctl(%q) = (%v, %q), want ok=%v containing %q",
					tc.out, ok, detail, tc.wantOK, tc.wantSub)
			}
		})
	}
}

func TestInterpretSc(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		runErr  error
		wantOK  bool
		wantSub string
	}{
		{"running", "        STATE              : 4  RUNNING", nil, true, "running"},
		{"stopped", "        STATE              : 1  STOPPED", nil, false, "not running"},
		{"start pending", "        STATE              : 2  START_PENDING", nil, true, "running"},
		{"not found", "[SC] EnumQueryServicesStatus:OpenService FAILED 1060:", errors.New("exit 1"), false, "not found"},
		{"garbage", "???", nil, true, "inconclusive"},
		// German Windows: state words are localized, the numeric code is not.
		{"german running", "        ZUSTAND            : 4  WIRD AUSGEFÜHRT", nil, true, "running"},
		{"german stopped", "        ZUSTAND            : 1  BEENDET", nil, false, "not running"},
		// PID / TYPE numbers on other lines must not be mistaken for a state.
		{"only pid line", "        PROCESS_ID         : 1234", nil, true, "inconclusive"},
		// paused (7) is ambiguous → inconclusive, not a false "stopped".
		{"paused", "        STATE              : 7  PAUSED", nil, true, "inconclusive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := interpretSc(tc.out, tc.runErr)
			if ok != tc.wantOK || !strings.Contains(detail, tc.wantSub) {
				t.Errorf("interpretSc = (%v, %q), want ok=%v containing %q",
					ok, detail, tc.wantOK, tc.wantSub)
			}
		})
	}
}

func TestParseScKeyName(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"english success", "[SC] GetServiceKeyName SUCCESS\n\nName = tEXAMServer\n", "tEXAMServer"},
		{"german success", "[SC] GetServiceKeyName ERFOLG\nName = tEXAMServer", "tEXAMServer"},
		{"no spaces around eq", "Name=tEXAMServer", "tEXAMServer"},
		{"failure header only", "[SC] GetServiceKeyName FAILED 1060:", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseScKeyName(tc.out); got != tc.want {
				t.Errorf("parseScKeyName(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// probeServiceRunning must never report a hard failure for an inconclusive
// situation: an unparseable / unknown stop command yields ok=true.
func TestProbeServiceRunning_InconclusiveIsOK(t *testing.T) {
	for _, stopCmd := range []string{"", "true", "myctl stop x"} {
		ok, detail := probeServiceRunning("linux", stopCmd)
		if !ok {
			t.Errorf("probeServiceRunning(%q) ok=false, want true (inconclusive)", stopCmd)
		}
		if !strings.Contains(detail, "inconclusive") {
			t.Errorf("probeServiceRunning(%q) detail=%q, want inconclusive", stopCmd, detail)
		}
	}
}
