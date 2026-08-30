package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The command is shell, so it is run as shell rather than matched as a string.
func runAuthorizedKeyCommand(t *testing.T, home, entry string) string {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run the generated command with")
	}
	command, err := authorizedKeyCommand(entry)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", command)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func authorizedKeysOf(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAuthorizedKeyIsAddedToAnEmptyAccount(t *testing.T) {
	home := t.TempDir()
	entry := `command="docker start survival",no-pty ssh-ed25519 AAAAkeybody mcwod`

	if status := runAuthorizedKeyCommand(t, home, entry); status != "added" {
		t.Errorf("status = %q, want added", status)
	}
	if got := authorizedKeysOf(t, home); got != entry+"\n" {
		t.Errorf("authorized_keys = %q", got)
	}
}

// The old line named the container the watcher no longer starts, so leaving it
// in place is what made setup-ssh report success and change nothing.
func TestAuthorizedKeyReplacesAnOutdatedForcedCommand(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	other := "ssh-ed25519 AAAAsomeoneelse laptop"
	stale := `command="docker start old-world",no-pty ssh-ed25519 AAAAkeybody mcwod`
	if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"),
		[]byte(other+"\n"+stale+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := `command="docker start survival",no-pty ssh-ed25519 AAAAkeybody mcwod`
	if status := runAuthorizedKeyCommand(t, home, entry); status != "replaced" {
		t.Errorf("status = %q, want replaced", status)
	}

	got := authorizedKeysOf(t, home)
	if strings.Contains(got, "old-world") {
		t.Errorf("the outdated forced command survived:\n%s", got)
	}
	if !strings.Contains(got, other) {
		t.Errorf("someone else's key was dropped:\n%s", got)
	}
	if strings.Count(got, "AAAAkeybody") != 1 {
		t.Errorf("the key appears %d times:\n%s", strings.Count(got, "AAAAkeybody"), got)
	}
}

func TestAuthorizedKeyCommandRefusesAMalformedEntry(t *testing.T) {
	if _, err := authorizedKeyCommand("ssh-ed25519"); err == nil {
		t.Error("an entry without a key body was accepted")
	}
}
