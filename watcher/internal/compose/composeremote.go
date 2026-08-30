package compose

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
)

// Where the compose file lives on the server and what is already in it.
type ComposeTarget struct {
	Dir         string
	File        string
	EnvFile     string
	Existing    string
	ExistingEnv string
	// Empty when neither docker compose nor docker-compose is there.
	Command string
}

func (t ComposeTarget) Exists() bool {
	return strings.TrimSpace(t.Existing) != ""
}

// v2 is a docker subcommand, v1 a separate binary. Which one is there decides
// how every later call is spelled.
func detectComposeCommand(s *remote.ServerSession) string {
	if _, err := s.Run("docker compose version"); err == nil {
		return "docker compose"
	}
	if _, err := s.Run("docker-compose version"); err == nil {
		return "docker-compose"
	}
	return ""
}

func InspectComposeTarget(s *remote.ServerSession, dir string) ComposeTarget {
	target := ComposeTarget{
		Dir:     dir,
		File:    JoinRemote(s, dir, "docker-compose.yml"),
		EnvFile: JoinRemote(s, dir, ".env"),
		Command: detectComposeCommand(s),
	}
	target.Existing, _ = ReadRemoteFile(s, target.File)
	if target.Existing == "" {
		// compose accepts either name, so the other one has to be looked at too.
		alternate := JoinRemote(s, dir, "compose.yaml")
		if body, err := ReadRemoteFile(s, alternate); err == nil && strings.TrimSpace(body) != "" {
			target.File = alternate
			target.Existing = body
		}
	}
	target.ExistingEnv, _ = ReadRemoteFile(s, target.EnvFile)
	return target
}

func JoinRemote(s *remote.ServerSession, dir, name string) string {
	if s.Platform().Windows {
		return strings.TrimRight(dir, `\/`) + `\` + name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

func ReadRemoteFile(s *remote.ServerSession, path string) (string, error) {
	if s.Platform().Windows {
		return s.Run("if (Test-Path -LiteralPath " + remote.PowerShellQuote(path) +
			") { Get-Content -Raw -LiteralPath " + remote.PowerShellQuote(path) + " }")
	}
	return s.Run("cat " + remote.ShellQuote(path) + " 2>/dev/null")
}

// Nothing is overwritten without a copy of what was there first.
func BackupComposeFile(s *remote.ServerSession, target ComposeTarget) (string, error) {
	if !target.Exists() {
		return "", nil
	}
	backup := JoinRemote(s, target.Dir, composeBackupName(time.Now().UTC().Format("20060102-150405")))

	var command string
	if s.Platform().Windows {
		command = "Copy-Item -LiteralPath " + remote.PowerShellQuote(target.File) +
			" -Destination " + remote.PowerShellQuote(backup)
	} else {
		command = "cp -p " + remote.ShellQuote(target.File) + " " + remote.ShellQuote(backup)
	}
	if out, err := s.Run(command); err != nil {
		return "", fmt.Errorf("cannot back up %s: %w: %s", target.File, err, logging.Sanitize(out, 200))
	}
	return backup, nil
}

// Written to a temporary name and moved into place, so an interrupted write
// cannot leave a truncated compose file behind.
func WriteRemoteFile(s *remote.ServerSession, path, content string) error {
	marker := "MCWOLFILE"
	if s.Platform().Windows {
		command := fmt.Sprintf("$body = @'\n%s\n'@\nSet-Content -LiteralPath %s -Value $body -Encoding utf8",
			strings.TrimRight(content, "\n"), remote.PowerShellQuote(path+".tmp"))
		command += "\nMove-Item -Force -LiteralPath " + remote.PowerShellQuote(path+".tmp") +
			" -Destination " + remote.PowerShellQuote(path)
		if out, err := s.Run(command); err != nil {
			return fmt.Errorf("cannot write %s: %w: %s", path, err, logging.Sanitize(out, 200))
		}
		return nil
	}

	command := fmt.Sprintf("set -e; cat > %s <<'%s'\n%s\n%s\nmv %s %s",
		remote.ShellQuote(path+".tmp"), marker, strings.TrimRight(content, "\n"), marker,
		remote.ShellQuote(path+".tmp"), remote.ShellQuote(path))
	if out, err := s.Run(command); err != nil {
		return fmt.Errorf("cannot write %s: %w: %s", path, err, logging.Sanitize(out, 200))
	}
	return nil
}

// The RCON password is in there, so other accounts on the server must not read it.
func WriteRemoteEnvFile(s *remote.ServerSession, path, content string) error {
	if err := WriteRemoteFile(s, path, content); err != nil {
		return err
	}
	if s.Platform().Windows {
		out, err := s.Run("icacls " + remote.PowerShellQuote(path) + " /inheritance:r /grant:r " +
			"\"$env:USERNAME:(F)\" /grant \"Administrators:(F)\" /grant \"SYSTEM:(F)\"")
		if err != nil {
			logging.Warnf("Could not lock down %s: %v: %s", path, err, logging.Sanitize(out, 200))
		}
		return nil
	}
	if out, err := s.Run("chmod 600 " + remote.ShellQuote(path)); err != nil {
		return fmt.Errorf("cannot set the mode on %s: %w: %s", path, err, logging.Sanitize(out, 200))
	}
	return nil
}

// compose parses the file itself, which catches anything the generator got
// wrong before the old file is gone for good.
func ValidateComposeFile(s *remote.ServerSession, target ComposeTarget) error {
	if target.Command == "" {
		return fmt.Errorf("no docker compose on the server")
	}
	out, err := s.Run(ComposeInvocation(s, target, "config --quiet"))
	if err != nil {
		return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 400))
	}
	return nil
}

func ComposeUp(s *remote.ServerSession, target ComposeTarget) (string, error) {
	return s.Run(ComposeInvocation(s, target, "up -d"))
}

func ComposeInvocation(s *remote.ServerSession, target ComposeTarget, args string) string {
	if s.Platform().Windows {
		return "Set-Location -LiteralPath " + remote.PowerShellQuote(target.Dir) + "; " +
			target.Command + " -f " + remote.PowerShellQuote(target.File) + " " + args
	}
	return "cd " + remote.ShellQuote(target.Dir) + " && " + target.Command +
		" -f " + remote.ShellQuote(target.File) + " " + args
}

// Newest first, which is what someone restoring almost always wants.
func ListComposeBackups(s *remote.ServerSession, dir string) ([]string, error) {
	var command string
	if s.Platform().Windows {
		command = "Get-ChildItem -LiteralPath " + remote.PowerShellQuote(dir) + " -Filter " +
			remote.PowerShellQuote(composeBackupPrefix+"*") + " | Select-Object -ExpandProperty Name"
	} else {
		command = "ls -1 " + remote.ShellQuote(dir) + " 2>/dev/null | grep '^" + composeBackupPrefix + "'"
	}

	out, err := s.Run(command)
	if err != nil {
		return nil, nil
	}
	names := filterComposeBackups(strings.Fields(out))
	sortComposeBackups(names)
	return names, nil
}

func composeBackupName(stamp string) string {
	return composeBackupPrefix + stamp
}

func filterComposeBackups(lines []string) []string {
	names := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, composeBackupPrefix) {
			names = append(names, line)
		}
	}
	return names
}

func sortComposeBackups(names []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
}
