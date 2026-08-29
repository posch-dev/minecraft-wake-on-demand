package remote

import (
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func TestRemoteHelperScriptOnlyAllowsTheKnownVerbs(t *testing.T) {
	script := RemoteHelperScriptUnix("minecraft", "", "sudo -n /usr/bin/systemctl suspend")

	for _, verb := range []string{
		RemoteVerbHello, remoteVerbStart, remoteVerbStop,
		RemoteVerbStatus, RemoteVerbPlayers, RemoteVerbSleep,
	} {
		if !strings.Contains(script, verb+")") {
			t.Errorf("the script has no branch for %q", verb)
		}
	}
	if !strings.Contains(script, "*)") || !strings.Contains(script, "exit 1") {
		t.Error("the script must refuse anything that is not a known verb")
	}
	// Nothing the watcher sends may reach a command, only the fixed branches.
	if strings.Contains(script, "$SSH_ORIGINAL_COMMAND ") {
		t.Error("SSH_ORIGINAL_COMMAND must never be executed, only matched")
	}
}

func TestRemoteHelperScriptLeavesOutSleepWhenNotWanted(t *testing.T) {
	script := RemoteHelperScriptUnix("minecraft", "", "")

	if strings.Contains(script, RemoteVerbSleep+")") {
		t.Error("without a sleep command the script must not offer the sleep verb")
	}
	if !strings.Contains(script, remoteVerbStart+")") {
		t.Error("the other verbs should still be there")
	}
}

func TestRemoteHelperScriptQuotesTheContainerName(t *testing.T) {
	script := RemoteHelperScriptUnix("odd'; rm -rf /; echo '", "", "")

	if strings.Contains(script, "rm -rf /;") && !strings.Contains(script, `'\'''`) {
		t.Errorf("a container name with a quote in it broke out of the script:\n%s", script)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got, want := ShellQuote("it's"), `'it'\''s'`; got != want {
		t.Errorf("shellQuote = %s, want %s", got, want)
	}
}

func TestPowerShellQuoteDoublesSingleQuotes(t *testing.T) {
	if got, want := PowerShellQuote("it's"), "'it''s'"; got != want {
		t.Errorf("powerShellQuote = %s, want %s", got, want)
	}
}

func TestSudoersLineNamesOneExactCommand(t *testing.T) {
	line := sudoersLine("eliah", "/usr/bin/systemctl", "suspend")

	if line != "eliah ALL=(root) NOPASSWD: /usr/bin/systemctl suspend\n" {
		t.Errorf("sudoers line = %q", line)
	}
	// A wildcard would let the rule run any systemctl subcommand as root.
	if strings.ContainsAny(line, "*?") {
		t.Errorf("the sudoers rule must not contain a wildcard: %q", line)
	}
}

func TestSudoersLineFollowsTheAction(t *testing.T) {
	cases := map[string]string{"suspend": "suspend", "hibernate": "hibernate", "shutdown": "poweroff"}
	for action, want := range cases {
		if line := sudoersLine("eliah", "/bin/systemctl", action); !strings.HasSuffix(line, want+"\n") {
			t.Errorf("action %q produced %q, want it to end in %q", action, line, want)
		}
	}
}

func TestSleepCommandUnixFallsBackToAKnownSystemctlPath(t *testing.T) {
	command, err := sleepCommandUnix("suspend", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if command != "sudo -n /usr/bin/systemctl suspend" {
		t.Errorf("command = %q", command)
	}
}

func TestSleepCommandRejectsAnUnknownAction(t *testing.T) {
	if _, err := sleepCommandUnix("explode", "", "/usr/bin/systemctl"); err == nil {
		t.Error("an unknown action should not produce a command")
	}
	if _, err := sleepCommandWindows("explode", ""); err == nil {
		t.Error("an unknown action should not produce a command")
	}
}

func TestDirectCommandRefusesSleepWithoutTheHelper(t *testing.T) {
	cfg := config.Default()
	cfg.Server.ContainerName = "minecraft"

	if _, err := directCommand(&cfg, RemoteVerbSleep); err == nil {
		t.Error("sleeping without the helper has no safe command, it must fail")
	}
	if got, _ := directCommand(&cfg, remoteVerbStart); got != "docker start minecraft" {
		t.Errorf("start = %q", got)
	}
	if got, _ := directCommand(&cfg, RemoteVerbPlayers); got != "docker exec minecraft rcon-cli list" {
		t.Errorf("players = %q", got)
	}
	if _, err := directCommand(&cfg, "reboot"); err == nil {
		t.Error("an unknown verb must not map to a command")
	}
}

func TestForcedCommandEntryLocksTheKeyDown(t *testing.T) {
	entry := RemoteHelperKeyEntryUnix("ssh-ed25519 AAAAexample")

	for _, want := range []string{
		`command="/usr/local/bin/mcwod-remote"`,
		"no-port-forwarding", "no-X11-forwarding", "no-agent-forwarding", "no-pty",
		"ssh-ed25519 AAAAexample",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("the entry is missing %q:\n%s", want, entry)
		}
	}
}

func TestSleepConfigNeedsTheHelper(t *testing.T) {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "192.168.1.100"
	cfg.Server.SSHUser = "eliah"
	cfg.WoL.BroadcastAddress = "192.168.1.255"
	cfg.Sleep.Enabled = true
	cfg.Server.RemoteHelper = false

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "remote_helper") {
		t.Errorf("sleep without the helper should be refused, got: %v", err)
	}

	cfg.Server.RemoteHelper = true
	if err := cfg.Validate(); err != nil {
		t.Errorf("sleep with the helper should validate: %v", err)
	}
}

func TestSleepConfigRejectsAnUnknownActionAndShortDelays(t *testing.T) {
	base := func() config.Config {
		cfg := config.Default()
		cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
		cfg.Server.IP = "192.168.1.100"
		cfg.Server.SSHUser = "eliah"
		cfg.WoL.BroadcastAddress = "192.168.1.255"
		cfg.Sleep.Enabled = true
		cfg.Server.RemoteHelper = true
		return cfg
	}

	cfg := base()
	cfg.Sleep.Action = "explode"
	if err := cfg.Validate(); err == nil {
		t.Error("an unknown sleep action should be refused")
	}

	cfg = base()
	cfg.Sleep.Action = "custom"
	cfg.Sleep.Command = ""
	if err := cfg.Validate(); err == nil {
		t.Error("a custom action without a command should be refused")
	}

	cfg = base()
	cfg.Sleep.IdleAfter = 5
	if err := cfg.Validate(); err == nil {
		t.Error("an idle_after below the floor should be refused")
	}
}

// docker start leaves the backup container behind, which is why backups stopped
// happening after every wake.
func TestStartBringsTheWholeComposeProjectUp(t *testing.T) {
	script := RemoteHelperScriptUnix("minecraft", "/srv/minecraft", "")

	for _, want := range []string{
		`COMPOSE_DIR='/srv/minecraft'`,
		`docker compose --project-directory "$COMPOSE_DIR" up -d`,
		`docker compose --project-directory "$COMPOSE_DIR" stop`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the helper is missing %q:\n%s", want, script)
		}
	}
}

// Someone who set the server up by hand has no compose directory recorded.
func TestWithoutAComposeDirectoryOnlyTheContainerIsStarted(t *testing.T) {
	script := RemoteHelperScriptUnix("minecraft", "", "")

	if !strings.Contains(script, "COMPOSE_DIR=''") {
		t.Errorf("the helper should carry an empty directory:\n%s", script)
	}
	if !strings.Contains(script, `exec docker start "$CONTAINER"`) {
		t.Errorf("the helper has no fallback:\n%s", script)
	}
}

// A restricted key without the helper runs one fixed command, so that command
// has to be the one that brings the project up.
func TestRestrictedKeyStartsTheComposeProject(t *testing.T) {
	entry := AuthorizedKeyEntry("ssh-ed25519 AAAAexample", "minecraft", "/srv/minecraft", true)
	if !strings.Contains(entry, "docker compose --project-directory '/srv/minecraft' up -d") {
		t.Errorf("forced command = %s", entry)
	}

	plain := AuthorizedKeyEntry("ssh-ed25519 AAAAexample", "minecraft", "", true)
	if !strings.Contains(plain, "docker start minecraft") {
		t.Errorf("forced command without a directory = %s", plain)
	}
}

func TestDirectStartUsesComposeWhenTheDirectoryIsKnown(t *testing.T) {
	cfg := config.Default()
	cfg.Server.ContainerName = "minecraft"
	cfg.Server.ComposeDir = "/srv/minecraft"

	command, err := directCommand(&cfg, remoteVerbStart)
	if err != nil {
		t.Fatal(err)
	}
	if command != "docker compose --project-directory '/srv/minecraft' up -d" {
		t.Errorf("start command = %q", command)
	}

	cfg.Server.ComposeDir = ""
	command, err = directCommand(&cfg, remoteVerbStart)
	if err != nil {
		t.Fatal(err)
	}
	if command != "docker start minecraft" {
		t.Errorf("start command without a directory = %q", command)
	}
}
