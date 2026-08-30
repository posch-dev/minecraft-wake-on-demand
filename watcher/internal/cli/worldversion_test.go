package cli

import (
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/players"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
)

const composeForVersionChange = `services:
  minecraft:
    image: itzg/minecraft-server:2026.8.0-java21
    environment:
      EULA: "TRUE"
      TYPE: "VANILLA"
      VERSION: "1.21.4"
      MEMORY: "4G"
      OPS: "eliah"
`

func TestSetWorldEnvironmentChangesOnlyTheTwoKeys(t *testing.T) {
	out, err := players.SetWorldEnvironment(composeForVersionChange, "minecraft", "FABRIC", "1.21.8")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`TYPE: "FABRIC"`, `VERSION: "1.21.8"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the change is missing %s:\n%s", want, out)
		}
	}
	for _, keep := range []string{`EULA: "TRUE"`, `MEMORY: "4G"`, `OPS: "eliah"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("the change lost %s:\n%s", keep, out)
		}
	}
}

func TestSetWorldEnvironmentUppercasesTheType(t *testing.T) {
	out, err := players.SetWorldEnvironment(composeForVersionChange, "minecraft", "fabric", "1.21.8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `TYPE: "FABRIC"`) {
		t.Errorf("the type was not normalised:\n%s", out)
	}
}

func TestSetWorldEnvironmentRefusesAMissingService(t *testing.T) {
	if _, err := players.SetWorldEnvironment(composeForVersionChange, "not-there", "PAPER", "1.21.4"); err == nil {
		t.Error("a service that is not there should be reported")
	}
}

// The backup runs on the server, the world never travels over SSH.
func TestBackupCommandStaysOnTheServer(t *testing.T) {
	unix := backupWorldCommand(&remote.ServerSession{}, "/srv/survival", "before-1.21.8.tar.gz")

	for _, want := range []string{"mkdir -p backups", "tar czf", "data"} {
		if !strings.Contains(unix, want) {
			t.Errorf("the backup command is missing %q:\n%s", want, unix)
		}
	}
	if strings.Contains(unix, "scp") || strings.Contains(unix, "cat ") {
		t.Errorf("the world must not be pulled through the connection:\n%s", unix)
	}
}

func TestBackupCommandOnWindows(t *testing.T) {
	windows := remote.NewSessionForPlatform(remote.ServerPlatform{Windows: true})
	command := backupWorldCommand(windows, `C:\srv\survival`, "before-1.21.8.tar.gz")

	if !strings.Contains(command, "Compress-Archive") {
		t.Errorf("Windows has no tar by default:\n%s", command)
	}
	if !strings.Contains(command, ".zip") {
		t.Errorf("Compress-Archive writes a zip, the name has to match:\n%s", command)
	}
}
