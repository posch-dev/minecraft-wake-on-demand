package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestStartContainerSendsDockerStart(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "")

	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Server.ContainerName = "mc-survival"

	runner := sshx.NewSSHRunner(&cfg)
	runner.SetPort(server.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := StartContainer(ctx, runner); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if got := server.LastCommand(); got != "docker start mc-survival" {
		t.Errorf("sent %q", got)
	}
}

func TestAuthorizedKeyEntry(t *testing.T) {
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample"

	restricted := AuthorizedKeyEntry(pub, "minecraft", "", true)
	for _, want := range []string{
		"command=\"docker start minecraft\"",
		"no-port-forwarding",
		"no-agent-forwarding",
		"no-pty",
		pub,
	} {
		if !strings.Contains(restricted, want) {
			t.Errorf("restricted entry is missing %q: %s", want, restricted)
		}
	}

	plain := AuthorizedKeyEntry(pub, "minecraft", "", false)
	if strings.Contains(plain, "command=") {
		t.Errorf("unrestricted entry should carry no command: %s", plain)
	}
	if !strings.HasPrefix(plain, pub) {
		t.Errorf("unrestricted entry should start with the key: %s", plain)
	}
}

// setup-ssh appends over a password login, and running it twice must not
// duplicate the line.
func TestInstallAuthorizedKeyIsIdempotent(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "correct-horse")

	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Server.ContainerName = "minecraft"

	// Pre-trust the host key so the prompt is never reached.
	line := knownhosts.Line([]string{knownhosts.Normalize(server.Addr)}, hostKey.PublicKey())
	if err := os.WriteFile(cfg.Server.SSHKnownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := sshx.NewSSHRunner(&cfg)
	runner.SetPort(server.Port())

	entry := AuthorizedKeyEntry("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample", "minecraft", "", true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p := ui.NewPrompterFrom(strings.NewReader(""))
	session, err := DialServerSession(ctx, runner, "correct-horse", p)
	if err != nil {
		t.Fatalf("DialServerSession: %v", err)
	}
	defer session.Close()

	if _, err := AppendAuthorizedKey(session, entry); err != nil {
		t.Fatalf("AppendAuthorizedKey: %v", err)
	}

	command := server.LastCommand()
	for _, want := range []string{
		"mkdir -p ~/.ssh",
		"chmod 700 ~/.ssh",
		"chmod 600 authorized_keys",
		"AAAAC3NzaC1lZDI1NTE5AAAAIexample",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("the remote command is missing %q:\n%s", want, command)
		}
	}
	// What the file is replaced with is built beside it and moved into place.
	if !strings.Contains(command, "mv authorized_keys.mcwod authorized_keys") {
		t.Errorf("the command does not swap the file in:\n%s", command)
	}
}
