package main

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

func setupSSHConfig(t *testing.T, server *testsupport.TestSSHServer, keyPath string) (*sshx.SSHRunner, string) {
	t.Helper()
	dir := filepath.Dir(keyPath)
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	knownHosts := filepath.Join(dir, "known_hosts")
	cfg.Server.SSHKnownHosts = knownHosts

	runner := sshx.NewSSHRunner(&cfg)
	runner.SetPort(server.Port())
	return runner, knownHosts
}

// accept-new lets the watcher learn a key silently. setup-ssh must not,
// somebody is sitting in front of it and was promised a fingerprint.
func TestSetupSSHAsksBeforeTrustingAnUnknownHost(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "correct-horse")
	runner, knownHosts := setupSSHConfig(t, server, keyPath)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Answering no has to abort and leave known_hosts empty.
	refuse := ui.NewPrompterFrom(strings.NewReader("n\n"))
	if _, err := DialServerSession(ctx, runner, "correct-horse", refuse); err == nil {
		t.Fatal("a refused host key must abort the setup")
	}
	if data, _ := os.ReadFile(knownHosts); len(strings.TrimSpace(string(data))) != 0 {
		t.Errorf("known_hosts was written despite the refusal: %s", data)
	}

	// Answering yes has to accept it and remember the key.
	accept := ui.NewPrompterFrom(strings.NewReader("y\n"))
	session, err := DialServerSession(ctx, runner, "correct-horse", accept)
	if err != nil {
		t.Fatalf("accepting the host key should succeed: %v", err)
	}
	session.Close()

	verify, err := knownhosts.New(knownHosts)
	if err != nil {
		t.Fatalf("known_hosts does not parse: %v", err)
	}
	if err := verify(server.Addr, server.Listener.Addr(), hostKey.PublicKey()); err != nil {
		t.Errorf("the confirmed key was not stored: %v", err)
	}
}

// A key that is already trusted must not trigger the question again.
func TestSetupSSHDoesNotAskForAKnownHost(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "correct-horse")
	runner, knownHosts := setupSSHConfig(t, server, keyPath)

	line := knownhosts.Line([]string{knownhosts.Normalize(server.Addr)}, hostKey.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// An empty prompter would block or refuse if a question were asked.
	silent := ui.NewPrompterFrom(strings.NewReader(""))
	session, err := DialServerSession(ctx, runner, "correct-horse", silent)
	if err != nil {
		t.Fatalf("a known host should not be questioned: %v", err)
	}
	defer session.Close()

	if _, err := appendAuthorizedKey(session, "ssh-ed25519 AAAAtest mcwod"); err != nil {
		t.Errorf("appending the key should work over an open session: %v", err)
	}
}

// Even with someone present, a changed key is never something to click through.
func TestSetupSSHRefusesAChangedHostKey(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	otherKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "correct-horse")
	runner, knownHosts := setupSSHConfig(t, server, keyPath)

	line := knownhosts.Line([]string{knownhosts.Normalize(server.Addr)}, otherKey.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Answering yes must not help here.
	eager := ui.NewPrompterFrom(strings.NewReader("y\ny\ny\n"))
	_, err := DialServerSession(ctx, runner, "correct-horse", eager)
	if err == nil {
		t.Fatal("a changed host key must abort even when confirmed")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Errorf("the error should name the change, got: %v", err)
	}
}

func TestSetupSSHRefusesAWrongPassword(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "correct-horse")
	runner, knownHosts := setupSSHConfig(t, server, keyPath)

	line := knownhosts.Line([]string{knownhosts.Normalize(server.Addr)}, hostKey.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p := ui.NewPrompterFrom(strings.NewReader(""))
	_, err := DialServerSession(ctx, runner, "wrong-password", p)
	if err == nil {
		t.Fatal("a wrong password must not install the key")
	}
	if !strings.Contains(err.Error(), "login as tester failed") {
		t.Errorf("got: %v", err)
	}
}

func TestEnsureKeyPairCreatesAnUnencryptedEd25519Key(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	signer, err := ensureKeyPair(path)
	if err != nil {
		t.Fatal(err)
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("key type = %s", signer.PublicKey().Type())
	}

	// It has to load back through the same path the watcher uses.
	reloaded, err := sshx.LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("the generated key does not load: %v", err)
	}
	if string(reloaded.PublicKey().Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Error("the reloaded key differs from the generated one")
	}

	if _, err := os.Stat(path + ".pub"); err != nil {
		t.Errorf("no public key written: %v", err)
	}

	// A second call must reuse it rather than overwrite it.
	again, err := ensureKeyPair(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.PublicKey().Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Error("the key was replaced on the second call")
	}
}
