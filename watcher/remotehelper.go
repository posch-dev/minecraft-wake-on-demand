package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The watcher sends one of these words and nothing else. The helper script on
// the server maps them to commands, so the key in authorized_keys never carries
// a command the watcher could vary, and a leaked key can do exactly this much.
const (
	remoteVerbHello   = "hello"
	remoteVerbStart   = "start"
	remoteVerbStop    = "stop"
	remoteVerbStatus  = "status"
	remoteVerbPlayers = "players"
	remoteVerbSleep   = "sleep"
	// Reports whether the network card is armed for the magic packet, which is
	// the one setting nothing else in the setup would reveal.
	remoteVerbWoLStatus = "wolstatus"
)

type wakeOnLANSetting int

const (
	wolUnknown wakeOnLANSetting = iota
	wolEnabled
	wolDisabled
)

// Answer to the hello verb, so check can tell the helper apart from an older
// key whose forced command silently runs docker start no matter what we send.
const remoteHelperMarker = "mc-wol-remote 1"

// The interface carrying the default route is the one the magic packet arrives
// on, so that is the one worth reporting.
const wolStatusCommandUnix = "iface=$(ip route show default | awk '{print $5; exit}'); " +
	"ethtool \"$iface\" 2>/dev/null | grep -i '^\t*Wake-on:'"

// PowerShell reports the same thing as ethtool, phrased differently. The output
// is normalised to a Wake-on: line so one parser covers both.
const wolStatusCommandWindows = `(Get-NetAdapter -Physical | Where-Object Status -eq 'Up' | ` +
	`Get-NetAdapterPowerManagement | ForEach-Object { if ($_.WakeOnMagicPacket -eq 'Enabled') ` +
	`{ 'Wake-on: g' } else { 'Wake-on: d' } }) | Select-Object -First 1`

const (
	remoteHelperPathUnix    = "/usr/local/bin/mc-wol-remote"
	remoteHelperPathWindows = `C:\ProgramData\mc-wol-proxy\mc-wol-remote.ps1`
	sudoersPath             = "/etc/sudoers.d/mc-wol-proxy"
)

// Commands the watcher sends when no helper is installed, which is the old
// setup and a plain unrestricted key. Sleeping is missing on purpose, it needs
// the sudoers line that only comes with the helper.
func directCommand(cfg *Config, verb string) (string, error) {
	container := cfg.Server.ContainerName
	switch verb {
	case remoteVerbStart:
		return "docker start " + container, nil
	case remoteVerbStop:
		return "docker stop " + container, nil
	case remoteVerbStatus:
		return "docker inspect -f {{.State.Status}} " + container, nil
	case remoteVerbPlayers:
		return "docker exec " + container + " rcon-cli list", nil
	case remoteVerbWoLStatus:
		return wolStatusCommandUnix, nil
	case remoteVerbSleep:
		return "", fmt.Errorf("sending the server PC to sleep needs the helper script, " +
			"run 'mc-wol-proxy setup-ssh' to install it")
	}
	return "", fmt.Errorf("unknown remote verb %q", verb)
}

// Absolute path so the sudoers rule can name it exactly. Filled in from the
// server during setup, since distributions disagree on /usr/bin and /bin.
func sleepCommandUnix(action, custom, systemctlPath string) (string, error) {
	if systemctlPath == "" {
		systemctlPath = "/usr/bin/systemctl"
	}
	switch action {
	case "suspend":
		return "sudo -n " + systemctlPath + " suspend", nil
	case "hibernate":
		return "sudo -n " + systemctlPath + " hibernate", nil
	case "shutdown":
		return "sudo -n " + systemctlPath + " poweroff", nil
	case "custom":
		return custom, nil
	}
	return "", fmt.Errorf("unknown sleep action %q", action)
}

// Windows has no sudo, these run with whatever rights the SSH session has. An
// administrator account is required for all three.
func sleepCommandWindows(action, custom string) (string, error) {
	switch action {
	case "suspend":
		return "rundll32.exe powrprof.dll,SetSuspendState 0,1,0", nil
	case "hibernate":
		return "shutdown.exe /h", nil
	case "shutdown":
		return "shutdown.exe /s /t 0", nil
	case "custom":
		return custom, nil
	}
	return "", fmt.Errorf("unknown sleep action %q", action)
}

// Only the systemctl subcommand we actually use is allowed, without a wildcard,
// so the rule cannot be talked into running anything else as root.
func sudoersLine(user, systemctlPath, action string) string {
	subcommand := map[string]string{
		"suspend": "suspend", "hibernate": "hibernate", "shutdown": "poweroff",
	}[action]
	return fmt.Sprintf("%s ALL=(root) NOPASSWD: %s %s\n", user, systemctlPath, subcommand)
}

func remoteHelperScriptUnix(containerName, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Forced command for the mc-wol-proxy key, installed by 'mc-wol-proxy setup-ssh'.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs, so a\n")
	b.WriteString("# stolen key cannot do more than what is listed here.\n")
	b.WriteString("set -eu\n\n")
	b.WriteString("CONTAINER=" + shellQuote(containerName) + "\n\n")
	b.WriteString("case \"${SSH_ORIGINAL_COMMAND:-}\" in\n")
	b.WriteString(remoteVerbHello + ")   echo " + shellQuote(remoteHelperMarker) + " ;;\n")
	b.WriteString(remoteVerbStart + ")   exec docker start \"$CONTAINER\" ;;\n")
	b.WriteString(remoteVerbStop + ")    exec docker stop \"$CONTAINER\" ;;\n")
	b.WriteString(remoteVerbStatus + ")  exec docker inspect -f '{{.State.Status}}' \"$CONTAINER\" ;;\n")
	b.WriteString(remoteVerbPlayers + ") exec docker exec \"$CONTAINER\" rcon-cli list ;;\n")
	b.WriteString(remoteVerbWoLStatus + ") " + wolStatusCommandUnix + " ;;\n")
	if sleepCommand != "" {
		// Unquoted on purpose, the command is several words and has to split.
		b.WriteString(remoteVerbSleep + ")   exec " + sleepCommand + " ;;\n")
	}
	b.WriteString("*)       echo 'mc-wol-remote: refused' >&2; exit 1 ;;\n")
	b.WriteString("esac\n")
	return b.String()
}

func remoteHelperScriptWindows(containerName, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("# Forced command for the mc-wol-proxy key.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs.\n")
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$container = " + powerShellQuote(containerName) + "\n\n")
	b.WriteString("switch ($env:SSH_ORIGINAL_COMMAND) {\n")
	b.WriteString("  '" + remoteVerbHello + "'   { " + powerShellQuote(remoteHelperMarker) + " }\n")
	b.WriteString("  '" + remoteVerbStart + "'   { docker start $container }\n")
	b.WriteString("  '" + remoteVerbStop + "'    { docker stop $container }\n")
	b.WriteString("  '" + remoteVerbStatus + "'  { docker inspect -f '{{.State.Status}}' $container }\n")
	b.WriteString("  '" + remoteVerbWoLStatus + "' { " + wolStatusCommandWindows + " }\n")
	b.WriteString("  '" + remoteVerbPlayers + "' { docker exec $container rcon-cli list }\n")
	if sleepCommand != "" {
		b.WriteString("  '" + remoteVerbSleep + "'   { " + sleepCommand + " }\n")
	}
	b.WriteString("  default { Write-Error 'mc-wol-remote: refused'; exit 1 }\n")
	b.WriteString("}\n")
	return b.String()
}

// The restricted form is what SECURITY.md recommends: even a leaked key can
// then only do what the forced command allows.
func authorizedKeyEntry(publicKey, containerName string, restrict bool) string {
	if !restrict {
		return publicKey + " mc-wol-proxy"
	}
	return forcedCommandEntry(publicKey, "docker start "+containerName)
}

func remoteHelperKeyEntryUnix(publicKey string) string {
	return forcedCommandEntry(publicKey, remoteHelperPathUnix)
}

func remoteHelperKeyEntryWindows(publicKey string) string {
	return forcedCommandEntry(publicKey,
		`powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `+remoteHelperPathWindows)
}

func forcedCommandEntry(publicKey, command string) string {
	return fmt.Sprintf("command=%q,no-port-forwarding,no-X11-forwarding,"+
		"no-agent-forwarding,no-pty %s mc-wol-proxy", command, publicKey)
}

// Single quoted with the one escape sh understands, so a container name can
// never break out of the generated script.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// The two shapes vanilla has used, "There are 3 of a max of 20 players online"
// and the older "There are 0/20 players online". Matching on the numbers rather
// than the sentence keeps it working on a server that is not running in
// English, where only the surrounding words change.
// The two shapes vanilla has used, "There are 3 of a max of 20 players online"
// and the older "There are 0/20 players online". The last pattern takes the
// first number in the line, which is what keeps a server running in another
// language readable, since only the words around the numbers change.
var playerCountPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(\d+)\s*(?:of a max of|/)\s*\d+`),
	regexp.MustCompile(`(?i)there are\s+(\d+)`),
	regexp.MustCompile(`^\D*?(\d+)\D`),
}

// Reports the number of players online. Not ok means the answer could not be
// read, which callers have to treat as "someone might be playing" rather than
// as zero.
func parsePlayerCount(output string) (int, bool) {
	for _, pattern := range playerCountPatterns {
		if match := pattern.FindStringSubmatch(output); match != nil {
			if count, err := strconv.Atoi(match[1]); err == nil {
				return count, true
			}
		}
	}
	return 0, false
}

// ethtool prints "Wake-on: g" when the card is armed for the magic packet and
// "Wake-on: d" when it is disabled entirely.
func parseWakeOnLANSetting(ethtoolOutput string) wakeOnLANSetting {
	_, value, found := strings.Cut(ethtoolOutput, "Wake-on:")
	if !found {
		return wolUnknown
	}
	value = strings.TrimSpace(firstLine(value))
	switch {
	case value == "":
		return wolUnknown
	case strings.ContainsAny(value, "gG"):
		return wolEnabled
	case value == "d" || value == "D":
		return wolDisabled
	}
	return wolUnknown
}
