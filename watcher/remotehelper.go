package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// The watcher sends one of these words and nothing else, the helper maps them.
// A leaked key can therefore do exactly this much and no more.
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
const remoteHelperMarker = "mcwod-remote 1"

// The interface carrying the default route is the one the magic packet arrives
// on, so that is the one worth reporting.
const wolStatusCommandUnix = "iface=$(ip route show default | awk '{print $5; exit}'); " +
	"ethtool \"$iface\" 2>/dev/null | grep -i '^\t*Wake-on:'"

// PowerShell reports the same thing as ethtool, phrased differently. The output
// is normalised to a Wake-on: line so one parser covers both.
const wolStatusCommandWindows = `(Get-NetAdapter -Physical | Where-Object Status -eq 'Up' | ` +
	`Get-NetAdapterPowerManagement | ForEach-Object { if ($_.WakeOnMagicPacket -eq 'Enabled') ` +
	`{ 'Wake-on: g' } else { 'Wake-on: d' } }) | Select-Object -First 1`

// What the helper answered before the rename, so check can name the reason
// instead of reporting an answer nobody recognises.
const legacyHelperMarker = "mc-wol-remote 1"

const (
	remoteHelperPathUnix    = "/usr/local/bin/mcwod-remote"
	remoteHelperPathWindows = `C:\ProgramData\mcwod\mcwod-remote.ps1`
	sudoersPath             = "/etc/sudoers.d/mcwod"
)

// Used when no helper is installed. Sleep is absent, it needs the sudoers line.
func directCommand(cfg *config.Config, verb string) (string, error) {
	container := cfg.Server.ContainerName
	switch verb {
	case remoteVerbStart:
		return composeCommandUnix(cfg, "up -d", "docker start "+container), nil
	case remoteVerbStop:
		return composeCommandUnix(cfg, "stop", "docker stop "+container), nil
	case remoteVerbStatus:
		return "docker inspect -f {{.State.Status}} " + container, nil
	case remoteVerbPlayers:
		return "docker exec " + container + " rcon-cli list", nil
	case remoteVerbWoLStatus:
		return wolStatusCommandUnix, nil
	case remoteVerbSleep:
		return "", fmt.Errorf("sending the server PC to sleep needs the helper script, " +
			"run 'mcwod setup-ssh' to install it")
	}
	return "", fmt.Errorf("unknown remote verb %q", verb)
}

// The backup container belongs to the same project, and docker start would
// leave it behind. Without a known compose directory there is only the one
// container to work with.
func composeCommandUnix(cfg *config.Config, subcommand, fallback string) string {
	dir := strings.TrimSpace(cfg.Server.ComposeDir)
	if dir == "" {
		return fallback
	}
	return "docker compose --project-directory " + shellQuote(dir) + " " + subcommand
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

// The directory is baked in but may be empty, so the script decides at run time.
func composeOrPlainUnix(subcommand, plain string) string {
	return `if [ -n "$COMPOSE_DIR" ]; then exec docker compose --project-directory "$COMPOSE_DIR" ` +
		subcommand + `; fi; exec ` + plain + ` "$CONTAINER"`
}

func composeOrPlainWindows(subcommand, plain string) string {
	return "if ($composeDir) { docker compose --project-directory $composeDir " + subcommand +
		" } else { " + plain + " $container }"
}

func remoteHelperScriptUnix(containerName, composeDir, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Forced command for the mcwod key, installed by 'mcwod setup-ssh'.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs, so a\n")
	b.WriteString("# stolen key cannot do more than what is listed here.\n")
	b.WriteString("set -eu\n\n")
	b.WriteString("CONTAINER=" + shellQuote(containerName) + "\n")
	b.WriteString("COMPOSE_DIR=" + shellQuote(composeDir) + "\n\n")
	b.WriteString("case \"${SSH_ORIGINAL_COMMAND:-}\" in\n")
	b.WriteString(remoteVerbHello + ")   echo " + shellQuote(remoteHelperMarker) + " ;;\n")
	b.WriteString(remoteVerbStart + ")   " + composeOrPlainUnix("up -d", "docker start") + " ;;\n")
	b.WriteString(remoteVerbStop + ")    " + composeOrPlainUnix("stop", "docker stop") + " ;;\n")
	b.WriteString(remoteVerbStatus + ")  exec docker inspect -f '{{.State.Status}}' \"$CONTAINER\" ;;\n")
	b.WriteString(remoteVerbPlayers + ") exec docker exec \"$CONTAINER\" rcon-cli list ;;\n")
	b.WriteString(remoteVerbWoLStatus + ") " + wolStatusCommandUnix + " ;;\n")
	if sleepCommand != "" {
		// Unquoted on purpose, the command is several words and has to split.
		b.WriteString(remoteVerbSleep + ")   exec " + sleepCommand + " ;;\n")
	}
	b.WriteString("*)       echo 'mcwod-remote: refused' >&2; exit 1 ;;\n")
	b.WriteString("esac\n")
	return b.String()
}

func remoteHelperScriptWindows(containerName, composeDir, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("# Forced command for the mcwod key.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs.\n")
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$container = " + powerShellQuote(containerName) + "\n")
	b.WriteString("$composeDir = " + powerShellQuote(composeDir) + "\n\n")
	b.WriteString("switch ($env:SSH_ORIGINAL_COMMAND) {\n")
	b.WriteString("  '" + remoteVerbHello + "'   { " + powerShellQuote(remoteHelperMarker) + " }\n")
	b.WriteString("  '" + remoteVerbStart + "'   { " + composeOrPlainWindows("up -d", "docker start") + " }\n")
	b.WriteString("  '" + remoteVerbStop + "'    { " + composeOrPlainWindows("stop", "docker stop") + " }\n")
	b.WriteString("  '" + remoteVerbStatus + "'  { docker inspect -f '{{.State.Status}}' $container }\n")
	b.WriteString("  '" + remoteVerbWoLStatus + "' { " + wolStatusCommandWindows + " }\n")
	b.WriteString("  '" + remoteVerbPlayers + "' { docker exec $container rcon-cli list }\n")
	if sleepCommand != "" {
		b.WriteString("  '" + remoteVerbSleep + "'   { " + sleepCommand + " }\n")
	}
	b.WriteString("  default { Write-Error 'mcwod-remote: refused'; exit 1 }\n")
	b.WriteString("}\n")
	return b.String()
}

// The restricted form is what SECURITY.md recommends: even a leaked key can
// then only do what the forced command allows.
func authorizedKeyEntry(publicKey, containerName, composeDir string, restrict bool) string {
	if !restrict {
		return publicKey + " mcwod"
	}
	if strings.TrimSpace(composeDir) != "" {
		return forcedCommandEntry(publicKey,
			"docker compose --project-directory "+shellQuote(composeDir)+" up -d")
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
		"no-agent-forwarding,no-pty %s mcwod", command, publicKey)
}

// Single quoted with the one escape sh understands, so a container name can
// never break out of the generated script.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Both vanilla shapes, then the first number in the line as a fallback.
// Matching numbers not words keeps a non-English server readable.
var playerCountPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(\d+)\s*(?:of a max of|/)\s*\d+`),
	regexp.MustCompile(`(?i)there are\s+(\d+)`),
	regexp.MustCompile(`^\D*?(\d+)\D`),
}

// Not ok means unreadable, which callers must treat as busy and not as empty.
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
