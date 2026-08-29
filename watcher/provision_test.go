package main

import (
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
)

func TestParseDockerPort(t *testing.T) {
	cases := map[string]int{
		"0.0.0.0:25565":                   25565,
		"0.0.0.0:25565\n[::]:25565":       25565,
		"192.168.1.100:25570":             25570,
		"":                                0,
		"Error: No public port for 25565": 0,
	}
	for out, want := range cases {
		if got := parseDockerPort(out); got != want {
			t.Errorf("parseDockerPort(%q) = %d, want %d", out, got, want)
		}
	}
}

func TestParseRCONEnabled(t *testing.T) {
	on := "EULA=TRUE\nENABLE_RCON=true\nRCON_PASSWORD=secret\nMEMORY=4G"
	if !parseRCONEnabled(on) {
		t.Error("ENABLE_RCON=true should read as enabled")
	}
	if !parseRCONEnabled("enable_rcon=TRUE") {
		t.Error("the variable name and value are both case insensitive")
	}

	for _, env := range []string{"EULA=TRUE\nMEMORY=4G", "ENABLE_RCON=false", ""} {
		if parseRCONEnabled(env) {
			t.Errorf("parseRCONEnabled(%q) should be false", env)
		}
	}
}

// A boot resets the driver setting on most distributions, so arming it once is
// not enough.
func TestWakeOnLANUnitArmsTheCardOnEveryBoot(t *testing.T) {
	unit := wakeOnLANUnit("enp3s0")

	for _, want := range []string{
		"ExecStart=/usr/sbin/ethtool -s enp3s0 wol g",
		"WantedBy=multi-user.target",
		"Type=oneshot",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit is missing %q:\n%s", want, unit)
		}
	}
	if got := wakeOnLANUnitPath("enp3s0"); got != "/etc/systemd/system/mcwod-arm@enp3s0.service" {
		t.Errorf("unit path = %q", got)
	}
}

func TestValidateHostOrIPAcceptsBothForms(t *testing.T) {
	for _, value := range []string{"192.168.1.100", "127.0.0.1", "localhost"} {
		if err := validateHostOrIP(value); err != nil {
			t.Errorf("validateHostOrIP(%q) = %v, want it accepted", value, err)
		}
	}
	for _, value := range []string{"", "   ", "not a host name at all"} {
		if err := validateHostOrIP(value); err == nil {
			t.Errorf("validateHostOrIP(%q) should have been rejected", value)
		}
	}
}

func TestValidateContainerName(t *testing.T) {
	for _, name := range []string{"minecraft", "mc-survival", "mc_1.21", "MC"} {
		if err := validateContainerName(name); err != nil {
			t.Errorf("validateContainerName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "-leading-dash", "has space", "semi;colon"} {
		if err := validateContainerName(name); err == nil {
			t.Errorf("validateContainerName(%q) should have been rejected", name)
		}
	}
}

// The commands the discovery sends must not be built by pasting a name in raw.
func TestDiscoveryQuotesTheInterfaceName(t *testing.T) {
	quoted := remote.ShellQuote("eth0'; rm -rf /; echo '")
	if strings.HasPrefix(quoted, "'eth0'") && !strings.Contains(quoted, `'\''`) {
		t.Errorf("an interface name with a quote in it escapes its quoting: %s", quoted)
	}
}
