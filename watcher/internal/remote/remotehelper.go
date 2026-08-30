package remote

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// The watcher sends one of these words and nothing else, the helper maps them.
// A leaked key can therefore do exactly this much and no more.
const (
	RemoteVerbHello   = "hello"
	remoteVerbStart   = "start"
	remoteVerbStop    = "stop"
	RemoteVerbStatus  = "status"
	RemoteVerbPlayers = "players"
	RemoteVerbSleep   = "sleep"
	// Reports whether the network card is armed for the magic packet, which is
	// the one setting nothing else in the setup would reveal.
	RemoteVerbWoLStatus = "wolstatus"
)

type WakeOnLANSetting int

const (
	WolUnknown WakeOnLANSetting = iota
	WolEnabled
	WolDisabled
)

// Answer to the hello verb, so check can tell the helper apart from an older
// key whose forced command silently runs docker start no matter what we send.
const RemoteHelperMarker = "mcwod-remote 1"

// The interface carrying the default route is the one the magic packet arrives
// on, so that is the one worth reporting.
const wolStatusCommandUnix = "iface=$(ip route show default | awk '{print $5; exit}'); " +
	"ethtool \"$iface\" 2>/dev/null | grep -i '^\t*Wake-on:'"

// PowerShell reports the same thing as ethtool, phrased differently. The output
// is normalised to a Wake-on: line so one parser covers both.
const WolStatusCommandWindows = `(Get-NetAdapter -Physical | Where-Object Status -eq 'Up' | ` +
	`Get-NetAdapterPowerManagement | ForEach-Object { if ($_.WakeOnMagicPacket -eq 'Enabled') ` +
	`{ 'Wake-on: g' } else { 'Wake-on: d' } }) | Select-Object -First 1`

// What the helper answered before the rename, so check can name the reason
// instead of reporting an answer nobody recognises.
const LegacyHelperMarker = "mc-wol-remote 1"

const (
	RemoteHelperPathUnix    = "/usr/local/bin/mcwod-remote"
	remoteHelperPathWindows = `C:\ProgramData\mcwod\mcwod-remote.ps1`
	SudoersPath             = "/etc/sudoers.d/mcwod"
)

// Used when no helper is installed. Sleep is absent, it needs the sudoers line.
func directCommand(cfg *config.Config, verb string) (string, error) {
	container := cfg.Server.ContainerName
	switch verb {
	case remoteVerbStart:
		return composeCommandUnix(cfg, "up -d", "docker start "+container), nil
	case remoteVerbStop:
		return composeCommandUnix(cfg, "stop", "docker stop "+container), nil
	case RemoteVerbStatus:
		return "docker inspect -f {{.State.Status}} " + container, nil
	case RemoteVerbPlayers:
		return "docker exec " + container + " rcon-cli list", nil
	case RemoteVerbWoLStatus:
		return wolStatusCommandUnix, nil
	case RemoteVerbSleep:
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
	return "docker compose --project-directory " + ShellQuote(dir) + " " + subcommand
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

func RemoteHelperScriptUnix(containerName, composeDir, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Forced command for the mcwod key, installed by 'mcwod setup-ssh'.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs, so a\n")
	b.WriteString("# stolen key cannot do more than what is listed here.\n")
	b.WriteString("set -eu\n\n")
	b.WriteString("CONTAINER=" + ShellQuote(containerName) + "\n")
	b.WriteString("COMPOSE_DIR=" + ShellQuote(composeDir) + "\n\n")
	b.WriteString("case \"${SSH_ORIGINAL_COMMAND:-}\" in\n")
	b.WriteString(RemoteVerbHello + ")   echo " + ShellQuote(RemoteHelperMarker) + " ;;\n")
	b.WriteString(remoteVerbStart + ")   " + composeOrPlainUnix("up -d", "docker start") + " ;;\n")
	b.WriteString(remoteVerbStop + ")    " + composeOrPlainUnix("stop", "docker stop") + " ;;\n")
	b.WriteString(RemoteVerbStatus + ")  exec docker inspect -f '{{.State.Status}}' \"$CONTAINER\" ;;\n")
	b.WriteString(RemoteVerbPlayers + ") exec docker exec \"$CONTAINER\" rcon-cli list ;;\n")
	b.WriteString(RemoteVerbWoLStatus + ") " + wolStatusCommandUnix + " ;;\n")
	if sleepCommand != "" {
		// Unquoted on purpose, the command is several words and has to split.
		b.WriteString(RemoteVerbSleep + ")   exec " + sleepCommand + " ;;\n")
	}
	b.WriteString("*)       echo 'mcwod-remote: refused' >&2; exit 1 ;;\n")
	b.WriteString("esac\n")
	return b.String()
}

func RemoteHelperScriptWindows(containerName, composeDir, sleepCommand string) string {
	var b strings.Builder
	b.WriteString("# Forced command for the mcwod key.\n")
	b.WriteString("# The watcher sends one of the words below and nothing else ever runs.\n")
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$container = " + PowerShellQuote(containerName) + "\n")
	b.WriteString("$composeDir = " + PowerShellQuote(composeDir) + "\n\n")
	b.WriteString("switch ($env:SSH_ORIGINAL_COMMAND) {\n")
	b.WriteString("  '" + RemoteVerbHello + "'   { " + PowerShellQuote(RemoteHelperMarker) + " }\n")
	b.WriteString("  '" + remoteVerbStart + "'   { " + composeOrPlainWindows("up -d", "docker start") + " }\n")
	b.WriteString("  '" + remoteVerbStop + "'    { " + composeOrPlainWindows("stop", "docker stop") + " }\n")
	b.WriteString("  '" + RemoteVerbStatus + "'  { docker inspect -f '{{.State.Status}}' $container }\n")
	b.WriteString("  '" + RemoteVerbWoLStatus + "' { " + WolStatusCommandWindows + " }\n")
	b.WriteString("  '" + RemoteVerbPlayers + "' { docker exec $container rcon-cli list }\n")
	if sleepCommand != "" {
		b.WriteString("  '" + RemoteVerbSleep + "'   { " + sleepCommand + " }\n")
	}
	b.WriteString("  default { Write-Error 'mcwod-remote: refused'; exit 1 }\n")
	b.WriteString("}\n")
	return b.String()
}

// The restricted form is what SECURITY.md recommends: even a leaked key can
// then only do what the forced command allows.
func AuthorizedKeyEntry(publicKey, containerName, composeDir string, restrict bool) string {
	if !restrict {
		return publicKey + " mcwod"
	}
	if strings.TrimSpace(composeDir) != "" {
		return forcedCommandEntry(publicKey,
			"docker compose --project-directory "+ShellQuote(composeDir)+" up -d")
	}
	return forcedCommandEntry(publicKey, "docker start "+containerName)
}

func RemoteHelperKeyEntryUnix(publicKey string) string {
	return forcedCommandEntry(publicKey, RemoteHelperPathUnix)
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
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func PowerShellQuote(value string) string {
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
func ParsePlayerCount(output string) (int, bool) {
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
func ParseWakeOnLANSetting(ethtoolOutput string) WakeOnLANSetting {
	_, value, found := strings.Cut(ethtoolOutput, "Wake-on:")
	if !found {
		return WolUnknown
	}
	value = strings.TrimSpace(FirstLine(value))
	switch {
	case value == "":
		return WolUnknown
	case strings.ContainsAny(value, "gG"):
		return WolEnabled
	case value == "d" || value == "D":
		return WolDisabled
	}
	return WolUnknown
}

// Every line carrying this key is dropped and the current one appended, so one
// key never ends up with two forced commands.
func AuthorizedKeyCommand(entry string) (string, error) {
	fields := strings.Fields(entry)
	if len(fields) < 2 {
		return "", fmt.Errorf("malformed authorized_keys entry")
	}
	keyBody := fields[len(fields)-2]

	return fmt.Sprintf(
		"set -e; cd ~/.ssh 2>/dev/null || { mkdir -p ~/.ssh; chmod 700 ~/.ssh; cd ~/.ssh; }; "+
			"touch authorized_keys; chmod 600 authorized_keys; "+
			"if grep -qF %[1]s authorized_keys; then echo replaced; else echo added; fi; "+
			"grep -vF %[1]s authorized_keys > authorized_keys.mcwod || true; "+
			"printf '%%s\\n' %[2]s >> authorized_keys.mcwod; "+
			"chmod 600 authorized_keys.mcwod; mv authorized_keys.mcwod authorized_keys",
		ShellQuote(keyBody), ShellQuote(entry)), nil
}

// Appended only when absent, so a second run does not duplicate the line.
// An older entry is left alone, which is why hello verifies afterwards.
// Reports whether the line was added or replaced, because skipping a key that
// is already there leaves an outdated forced command in place and the watcher
// then starts the wrong container.
func AppendAuthorizedKey(s *ServerSession, entry string) (string, error) {
	command, err := AuthorizedKeyCommand(entry)
	if err != nil {
		return "", err
	}
	out, err := s.Run(command)
	if err != nil {
		return "", fmt.Errorf("cannot write authorized_keys: %w: %s", err, logging.Sanitize(out, 200))
	}
	return strings.TrimSpace(out), nil
}
