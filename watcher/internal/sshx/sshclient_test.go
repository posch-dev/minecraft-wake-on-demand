package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestAcceptNewLearnsTheHostKey(t *testing.T) {
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
	knownHosts := filepath.Join(dir, "known_hosts")
	cfg.Server.SSHKnownHosts = knownHosts

	runner := NewSSHRunner(&cfg)
	runner.SetPort(server.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := runner.Run(ctx, "true"); err != nil {
		t.Fatalf("first connection failed: %v", err)
	}

	data, err := os.ReadFile(knownHosts)
	if err != nil || len(data) == 0 {
		t.Fatalf("known_hosts was not written: %v", err)
	}

	// The file has to be in the format OpenSSH reads.
	verify, err := knownhosts.New(knownHosts)
	if err != nil {
		t.Fatalf("known_hosts does not parse: %v", err)
	}
	addr, _ := net.ResolveTCPAddr("tcp", server.Addr)
	if err := verify(server.Addr, addr, hostKey.PublicKey()); err != nil {
		t.Errorf("the learned key does not validate: %v", err)
	}

	// A second run must reuse the entry and still succeed.
	if _, err := runner.Run(ctx, "true"); err != nil {
		t.Fatalf("second connection failed: %v", err)
	}
	after, _ := os.ReadFile(knownHosts)
	if bytes.Count(after, []byte("\n")) != bytes.Count(data, []byte("\n")) {
		t.Error("the host key was written a second time")
	}
}

func TestStrictYesRefusesAnUnknownHost(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := testsupport.WriteTestKey(t, dir)
	server := testsupport.StartTestSSHServer(t, hostKey, publicKey, "")

	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "yes"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")

	runner := NewSSHRunner(&cfg)
	runner.SetPort(server.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := runner.Run(ctx, "true")
	if err == nil {
		t.Fatal("an unknown host must be refused in 'yes' mode")
	}
	if !strings.Contains(err.Error(), "not in") {
		t.Errorf("the error should say the host is unknown, got: %v", err)
	}
	if data, _ := os.ReadFile(cfg.Server.SSHKnownHosts); len(data) != 0 {
		t.Error("'yes' mode must not write to known_hosts")
	}
}

// The case host key checking exists for.
func TestChangedHostKeyIsRejected(t *testing.T) {
	for _, mode := range []string{"accept-new", "yes"} {
		t.Run(mode, func(t *testing.T) {
			realKey := testsupport.NewHostKey(t)
			otherKey := testsupport.NewHostKey(t)

			dir := t.TempDir()
			keyPath, publicKey := testsupport.WriteTestKey(t, dir)
			server := testsupport.StartTestSSHServer(t, realKey, publicKey, "")

			// known_hosts claims a different key for this exact address.
			knownHosts := filepath.Join(dir, "known_hosts")
			line := knownhosts.Line([]string{knownhosts.Normalize(server.Addr)}, otherKey.PublicKey())
			if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg := config.Default()
			cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
			cfg.Server.IP = "127.0.0.1"
			cfg.Server.SSHUser = "tester"
			cfg.Server.SSHKeyPath = keyPath
			cfg.Server.SSHStrictHostKey = mode
			cfg.Server.SSHKnownHosts = knownHosts

			runner := NewSSHRunner(&cfg)
			runner.SetPort(server.Port())

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			_, err := runner.Run(ctx, "true")
			if err == nil {
				t.Fatal("a changed host key must never be accepted")
			}
			if !strings.Contains(err.Error(), "changed") {
				t.Errorf("the error should name the change, got: %v", err)
			}
		})
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	hostKey := testsupport.NewHostKey(t)
	dir := t.TempDir()
	keyPath, _ := testsupport.WriteTestKey(t, dir)
	// The server accepts a different key than the one the runner holds.
	_, otherPublic := testsupport.WriteTestKey(t, t.TempDir())
	server := testsupport.StartTestSSHServer(t, hostKey, otherPublic, "")

	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")

	runner := NewSSHRunner(&cfg)
	runner.SetPort(server.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := runner.Run(ctx, "true"); err == nil {
		t.Fatal("a key the server does not know must be refused")
	}
}

func TestPassphraseProtectedKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "test", []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadPrivateKey(path)
	if err == nil {
		t.Fatal("a passphrase protected key must be refused")
	}
	if !strings.Contains(err.Error(), "passphrase") {
		t.Errorf("the error should mention the passphrase, got: %v", err)
	}
}

func TestWorldReadableKeyIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not POSIX bits on Windows")
	}
	dir := t.TempDir()
	path, _ := testsupport.WriteTestKey(t, dir)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivateKey(path)
	if err == nil {
		t.Fatal("a world readable key must be refused")
	}
	if !strings.Contains(err.Error(), "readable by other users") {
		t.Errorf("got: %v", err)
	}
}

func TestMissingKeyExplainsHowToCreateOne(t *testing.T) {
	_, err := LoadPrivateKey(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing key must be an error")
	}
	if !strings.Contains(err.Error(), "ssh-keygen") {
		t.Errorf("the error should say how to create one, got: %v", err)
	}
}
