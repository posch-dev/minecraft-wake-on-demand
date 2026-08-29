package main

import (
	"fmt"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// Staged as the user, then moved by one sudo call, so only the password
// goes to sudo over stdin.
func installRemoteHelperUnix(s *ServerSession, cfg *config.Config) error {
	sleepCommand := ""
	if cfg.Sleep.Action != "" {
		command, err := sleepCommandUnix(cfg.Sleep.Action, cfg.Sleep.Command, s.platform.SystemctlPath)
		if err != nil {
			return err
		}
		sleepCommand = command
	}

	script := remoteHelperScriptUnix(cfg.Server.ContainerName, cfg.Server.ComposeDir, sleepCommand)
	staged, err := stageFile(s, "mcwod-remote", script)
	if err != nil {
		return err
	}

	install := fmt.Sprintf("install -o root -g root -m 0755 %s %s",
		shellQuote(staged), shellQuote(remoteHelperPathUnix))

	// Only a systemctl based action needs a rule, a custom command is the
	// user's own business and they arrange its rights themselves.
	needsSudoers := cfg.Sleep.Action != "" && cfg.Sleep.Action != "custom"
	if needsSudoers {
		line := sudoersLine(cfg.Server.SSHUser, s.platform.SystemctlPath, cfg.Sleep.Action)
		stagedSudoers, err := stageFile(s, "mcwod-sudoers", line)
		if err != nil {
			return err
		}
		// A broken file in sudoers.d locks the user out of their own machine,
		// so it is checked before it is allowed anywhere near /etc.
		install += fmt.Sprintf(" && visudo -c -q -f %s && install -o root -g root -m 0440 %s %s",
			shellQuote(stagedSudoers), shellQuote(stagedSudoers), shellQuote(sudoersPath))
	}

	if out, err := s.RunSudo("set -e; " + install); err != nil {
		return fmt.Errorf("cannot install the helper: %w: %s", err, logging.Sanitize(out, 300))
	}

	// The staged copies are removable by the user, the installed ones are not.
	s.Run("rm -f " + shellQuote(staged))
	return nil
}

// mktemp picks the name, so there is no predictable path in /tmp for someone
// else on the box to point a symlink at.
func stageFile(s *ServerSession, label, content string) (string, error) {
	marker := "MCWOL_" + strings.ToUpper(label)
	command := fmt.Sprintf(
		"set -e; path=$(mktemp); umask 077; cat > \"$path\" <<'%s'\n%s\n%s\nprintf '%%s\n' \"$path\"",
		marker, strings.TrimRight(content, "\n"), marker)

	out, err := s.Run(command)
	if err != nil {
		return "", fmt.Errorf("cannot stage %s: %w: %s", label, err, logging.Sanitize(out, 200))
	}
	path := lastLine(out)
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("cannot stage %s, the server answered %q", label, logging.Sanitize(out, 200))
	}
	return path, nil
}

func lastLine(value string) string {
	lines := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// Windows has no unattended way to gain administrator rights over SSH, so the
// script is printed for the user to place themselves.
func windowsHelperInstructions(cfg *config.Config, publicKey string) string {
	sleepCommand, err := sleepCommandWindows(cfg.Sleep.Action, cfg.Sleep.Command)
	if err != nil {
		sleepCommand = ""
	}
	script := remoteHelperScriptWindows(cfg.Server.ContainerName, cfg.Server.ComposeDir, sleepCommand)

	var b strings.Builder
	b.WriteString("\nOn a Windows server PC, do these three steps in an administrator PowerShell.\n")
	b.WriteString("\n1. Save this as " + remoteHelperPathWindows + "\n\n")
	b.WriteString(script)
	b.WriteString("\n2. Take write access away from everyone but administrators, otherwise\n")
	b.WriteString("   anyone holding the key could rewrite the script and run anything:\n\n")
	b.WriteString("icacls " + remoteHelperPathWindows + " /inheritance:r " +
		"/grant \"Administrators:(F)\" /grant \"SYSTEM:(F)\" /grant \"Users:(RX)\"\n")
	b.WriteString("\n3. Add this single line to authorized_keys:\n\n")
	b.WriteString(remoteHelperKeyEntryWindows(publicKey) + "\n")
	b.WriteString("\n" + windowsAuthorizedKeysNote(cfg.Server.SSHUser))
	return b.String()
}

// Windows OpenSSH ignores the profile authorized_keys for admin accounts,
// the usual reason a correct looking key is never accepted.
func windowsAuthorizedKeysNote(user string) string {
	return fmt.Sprintf(`The file is C:\Users\%s\.ssh\authorized_keys for a normal account.
If the account is an administrator, Windows OpenSSH reads
C:\ProgramData\ssh\administrators_authorized_keys instead and ignores
the one in the profile. That file has to be owned by Administrators or SYSTEM:

icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r /grant "Administrators:(F)" /grant "SYSTEM:(F)"
`, user)
}
