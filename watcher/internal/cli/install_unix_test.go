//go:build !windows

package cli

import (
	"strings"
	"testing"
)

func TestTheUnitFollowsTheInstallDirAndTheUser(t *testing.T) {
	unit := renderSystemdUnit("/srv/mcwod", "pi")

	if strings.Contains(unit, "MCWOD_USER") {
		t.Error("the user placeholder was left in the unit")
	}
	if !strings.Contains(unit, "User=pi") {
		t.Error("the unit does not run as the invoking user")
	}
	if strings.Contains(unit, "/opt/mcwod") {
		t.Errorf("the default path survived:\n%s", unit)
	}
	for _, want := range []string{
		"ExecStart=/srv/mcwod/mcwod run",
		"Environment=MCWOD_CONFIG=/srv/mcwod/config.yml",
		"Environment=SERVER_SSH_KNOWN_HOSTS=/srv/mcwod/known_hosts",
		"ReadWritePaths=/srv/mcwod",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("missing %q in:\n%s", want, unit)
		}
	}
}

// Installed to the default place the unit has to come out unchanged, which is
// what the shipped file is tested against everywhere else.
func TestTheDefaultInstallLeavesTheUnitAlone(t *testing.T) {
	unit := renderSystemdUnit(defaultInstallDir(), "pi")
	if !strings.Contains(unit, "ExecStart=/opt/mcwod/mcwod run") {
		t.Errorf("the default path was rewritten:\n%s", unit)
	}
}
