package main

import (
	"strings"
	"testing"
)

func TestParsePlayerCountReadsBothVanillaShapes(t *testing.T) {
	cases := map[string]int{
		"There are 3 of a max of 20 players online: alice, bob, carol": 3,
		"There are 0 of a max of 20 players online:":                   0,
		"There are 0/20 players online:":                               0,
		"There are 12/50 players online: someone":                      12,
		// The numbers carry the meaning, so a translated server still works.
		"Es sind 4 von maximal 20 Spielern online: a, b, c, d": 4,
	}
	for output, want := range cases {
		got, ok := parsePlayerCount(output)
		if !ok {
			t.Errorf("parsePlayerCount(%q) could not read a count", output)
			continue
		}
		if got != want {
			t.Errorf("parsePlayerCount(%q) = %d, want %d", output, got, want)
		}
	}
}

// An unreadable answer must not look like an empty server, that would send the
// PC to sleep under the players.
func TestParsePlayerCountReportsWhenItCannotTell(t *testing.T) {
	for _, output := range []string{"", "Unknown command", "rcon: connection refused"} {
		if count, ok := parsePlayerCount(output); ok {
			t.Errorf("parsePlayerCount(%q) = %d, ok, but it should admit it cannot tell", output, count)
		}
	}
}

func TestParseWakeOnLANSetting(t *testing.T) {
	cases := map[string]wakeOnLANSetting{
		"\tWake-on: g":                          wolEnabled,
		"        Wake-on: pumbg":                wolEnabled,
		"Wake-on: d":                            wolDisabled,
		"Wake-on: D":                            wolDisabled,
		"Wake-on:":                              wolUnknown,
		"Settings for eth0:":                    wolUnknown,
		"":                                      wolUnknown,
		"Supports Wake-on: pumbg\n\tWake-on: d": wolEnabled,
	}
	for output, want := range cases {
		if got := parseWakeOnLANSetting(output); got != want {
			t.Errorf("parseWakeOnLANSetting(%q) = %d, want %d", output, got, want)
		}
	}
}

func TestWoLStatusVerbIsInBothHelperScripts(t *testing.T) {
	unix := remoteHelperScriptUnix("minecraft", "", "")
	windows := remoteHelperScriptWindows("minecraft", "", "")

	if !containsAll(unix, remoteVerbWoLStatus, "ethtool") {
		t.Errorf("the sh helper cannot report the Wake-on-LAN setting:\n%s", unix)
	}
	if !containsAll(windows, remoteVerbWoLStatus, "WakeOnMagicPacket") {
		t.Errorf("the PowerShell helper cannot report the Wake-on-LAN setting:\n%s", windows)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}
