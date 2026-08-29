package main

import (
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
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
		got, ok := remote.ParsePlayerCount(output)
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
		if count, ok := remote.ParsePlayerCount(output); ok {
			t.Errorf("parsePlayerCount(%q) = %d, ok, but it should admit it cannot tell", output, count)
		}
	}
}

func TestParseWakeOnLANSetting(t *testing.T) {
	cases := map[string]remote.WakeOnLANSetting{
		"\tWake-on: g":                          remote.WolEnabled,
		"        Wake-on: pumbg":                remote.WolEnabled,
		"Wake-on: d":                            remote.WolDisabled,
		"Wake-on: D":                            remote.WolDisabled,
		"Wake-on:":                              remote.WolUnknown,
		"Settings for eth0:":                    remote.WolUnknown,
		"":                                      remote.WolUnknown,
		"Supports Wake-on: pumbg\n\tWake-on: d": remote.WolEnabled,
	}
	for output, want := range cases {
		if got := remote.ParseWakeOnLANSetting(output); got != want {
			t.Errorf("parseWakeOnLANSetting(%q) = %d, want %d", output, got, want)
		}
	}
}

func TestWoLStatusVerbIsInBothHelperScripts(t *testing.T) {
	unix := remote.RemoteHelperScriptUnix("minecraft", "", "")
	windows := remote.RemoteHelperScriptWindows("minecraft", "", "")

	if !containsAll(unix, remote.RemoteVerbWoLStatus, "ethtool") {
		t.Errorf("the sh helper cannot report the Wake-on-LAN setting:\n%s", unix)
	}
	if !containsAll(windows, remote.RemoteVerbWoLStatus, "WakeOnMagicPacket") {
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

// A restricted key runs docker start whatever it is sent, and that prints the
// container name, which check used to show as the container's state.
func TestAContainerNameIsNotAContainerState(t *testing.T) {
	for _, state := range []string{"created", "restarting", "running", "removing", "paused", "exited", "dead"} {
		if !dockerContainerStates[state] {
			t.Errorf("%q is a state docker reports", state)
		}
	}
	for _, notAState := range []string{"fh-minecraft", "minecraft", "survival", "mcwod-remote 1"} {
		if dockerContainerStates[notAState] {
			t.Errorf("%q was taken for a state", notAState)
		}
	}
}
