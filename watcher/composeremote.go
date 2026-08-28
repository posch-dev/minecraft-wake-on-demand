package main

import (
	"fmt"
	"strings"
	"time"
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
func detectComposeCommand(s *ServerSession) string {
	if _, err := s.Run("docker compose version"); err == nil {
		return "docker compose"
	}
	if _, err := s.Run("docker-compose version"); err == nil {
		return "docker-compose"
	}
	return ""
}

func inspectComposeTarget(s *ServerSession, dir string) ComposeTarget {
	target := ComposeTarget{
		Dir:     dir,
		File:    joinRemote(s, dir, "docker-compose.yml"),
		EnvFile: joinRemote(s, dir, ".env"),
		Command: detectComposeCommand(s),
	}
	target.Existing, _ = readRemoteFile(s, target.File)
	if target.Existing == "" {
		// compose accepts either name, so the other one has to be looked at too.
		alternate := joinRemote(s, dir, "compose.yaml")
		if body, err := readRemoteFile(s, alternate); err == nil && strings.TrimSpace(body) != "" {
			target.File = alternate
			target.Existing = body
		}
	}
	target.ExistingEnv, _ = readRemoteFile(s, target.EnvFile)
	return target
}

func joinRemote(s *ServerSession, dir, name string) string {
	if s.Platform().Windows {
		return strings.TrimRight(dir, `\/`) + `\` + name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

func readRemoteFile(s *ServerSession, path string) (string, error) {
	if s.Platform().Windows {
		return s.Run("if (Test-Path -LiteralPath " + powerShellQuote(path) +
			") { Get-Content -Raw -LiteralPath " + powerShellQuote(path) + " }")
	}
	return s.Run("cat " + shellQuote(path) + " 2>/dev/null")
}

// Nothing is overwritten without a copy of what was there first.
func backupComposeFile(s *ServerSession, target ComposeTarget) (string, error) {
	if !target.Exists() {
		return "", nil
	}
	backup := joinRemote(s, target.Dir, composeBackupPrefix+time.Now().UTC().Format("20060102-150405"))

	var command string
	if s.Platform().Windows {
		command = "Copy-Item -LiteralPath " + powerShellQuote(target.File) +
			" -Destination " + powerShellQuote(backup)
	} else {
		command = "cp -p " + shellQuote(target.File) + " " + shellQuote(backup)
	}
	if out, err := s.Run(command); err != nil {
		return "", fmt.Errorf("cannot back up %s: %w: %s", target.File, err, sanitizeForLog(out, 200))
	}
	return backup, nil
}

// Written to a temporary name and moved into place, so an interrupted write
// cannot leave a truncated compose file behind.
func writeRemoteFile(s *ServerSession, path, content string) error {
	marker := "MCWOLFILE"
	if s.Platform().Windows {
		command := fmt.Sprintf("$body = @'\n%s\n'@\nSet-Content -LiteralPath %s -Value $body -Encoding utf8",
			strings.TrimRight(content, "\n"), powerShellQuote(path+".tmp"))
		command += "\nMove-Item -Force -LiteralPath " + powerShellQuote(path+".tmp") +
			" -Destination " + powerShellQuote(path)
		if out, err := s.Run(command); err != nil {
			return fmt.Errorf("cannot write %s: %w: %s", path, err, sanitizeForLog(out, 200))
		}
		return nil
	}

	command := fmt.Sprintf("set -e; cat > %s <<'%s'\n%s\n%s\nmv %s %s",
		shellQuote(path+".tmp"), marker, strings.TrimRight(content, "\n"), marker,
		shellQuote(path+".tmp"), shellQuote(path))
	if out, err := s.Run(command); err != nil {
		return fmt.Errorf("cannot write %s: %w: %s", path, err, sanitizeForLog(out, 200))
	}
	return nil
}

// The RCON password is in there, so other accounts on the server must not read it.
func writeRemoteEnvFile(s *ServerSession, path, content string) error {
	if err := writeRemoteFile(s, path, content); err != nil {
		return err
	}
	if s.Platform().Windows {
		out, err := s.Run("icacls " + powerShellQuote(path) + " /inheritance:r /grant:r " +
			"\"$env:USERNAME:(F)\" /grant \"Administrators:(F)\" /grant \"SYSTEM:(F)\"")
		if err != nil {
			log.Warnf("Could not lock down %s: %v: %s", path, err, sanitizeForLog(out, 200))
		}
		return nil
	}
	if out, err := s.Run("chmod 600 " + shellQuote(path)); err != nil {
		return fmt.Errorf("cannot set the mode on %s: %w: %s", path, err, sanitizeForLog(out, 200))
	}
	return nil
}

// compose parses the file itself, which catches anything the generator got
// wrong before the old file is gone for good.
func validateComposeFile(s *ServerSession, target ComposeTarget) error {
	if target.Command == "" {
		return fmt.Errorf("no docker compose on the server")
	}
	out, err := s.Run(composeInvocation(s, target, "config --quiet"))
	if err != nil {
		return fmt.Errorf("%w: %s", err, sanitizeForLog(out, 400))
	}
	return nil
}

func composeUp(s *ServerSession, target ComposeTarget) (string, error) {
	return s.Run(composeInvocation(s, target, "up -d"))
}

func composeInvocation(s *ServerSession, target ComposeTarget, args string) string {
	if s.Platform().Windows {
		return "Set-Location -LiteralPath " + powerShellQuote(target.Dir) + "; " +
			target.Command + " -f " + powerShellQuote(target.File) + " " + args
	}
	return "cd " + shellQuote(target.Dir) + " && " + target.Command +
		" -f " + shellQuote(target.File) + " " + args
}
