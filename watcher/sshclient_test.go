package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// A real SSH server in the test process, so the host key policy is exercised
// against an actual handshake rather than only read.
type testSSHServer struct {
	addr     string
	hostKey  ssh.Signer
	mu       sync.Mutex
	commands []string
	listener net.Listener
}

func newHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func writeTestKey(t *testing.T, dir string) (string, ssh.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return path, sshPub
}

func startTestSSHServer(t *testing.T, hostKey ssh.Signer, accept ssh.PublicKey, password string) *testSSHServer {
	t.Helper()

	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if accept != nil && bytes.Equal(key.Marshal(), accept.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errKeyRefused
		},
	}
	if password != "" {
		serverCfg.PasswordCallback = func(_ ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
			if string(given) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, errKeyRefused
		}
	}
	serverCfg.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &testSSHServer{addr: listener.Addr().String(), hostKey: hostKey, listener: listener}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.handle(conn, serverCfg)
		}
	}()
	return server
}

func (s *testSSHServer) handle(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}
		go func() {
			defer channel.Close()
			for req := range requests {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				// The payload is a length prefixed string.
				var payload struct{ Command string }
				ssh.Unmarshal(req.Payload, &payload)
				s.mu.Lock()
				s.commands = append(s.commands, payload.Command)
				s.mu.Unlock()

				req.Reply(true, nil)
				channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}()
	}
}

func (s *testSSHServer) lastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.commands) == 0 {
		return ""
	}
	return s.commands[len(s.commands)-1]
}

func (s *testSSHServer) port() int {
	_, p, _ := net.SplitHostPort(s.addr)
	n, _ := strconv.Atoi(p)
	return n
}

func TestAcceptNewLearnsTheHostKey(t *testing.T) {
	hostKey := newHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := writeTestKey(t, dir)
	server := startTestSSHServer(t, hostKey, publicKey, "")

	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	knownHosts := filepath.Join(dir, "known_hosts")
	cfg.Server.SSHKnownHosts = knownHosts

	runner := NewSSHRunner(&cfg)
	runner.port = server.port()

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
	addr, _ := net.ResolveTCPAddr("tcp", server.addr)
	if err := verify(server.addr, addr, hostKey.PublicKey()); err != nil {
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
	hostKey := newHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := writeTestKey(t, dir)
	server := startTestSSHServer(t, hostKey, publicKey, "")

	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "yes"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")

	runner := NewSSHRunner(&cfg)
	runner.port = server.port()

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
			realKey := newHostKey(t)
			otherKey := newHostKey(t)

			dir := t.TempDir()
			keyPath, publicKey := writeTestKey(t, dir)
			server := startTestSSHServer(t, realKey, publicKey, "")

			// known_hosts claims a different key for this exact address.
			knownHosts := filepath.Join(dir, "known_hosts")
			line := knownhosts.Line([]string{knownhosts.Normalize(server.addr)}, otherKey.PublicKey())
			if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg := defaultConfig()
			cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
			cfg.Server.IP = "127.0.0.1"
			cfg.Server.SSHUser = "tester"
			cfg.Server.SSHKeyPath = keyPath
			cfg.Server.SSHStrictHostKey = mode
			cfg.Server.SSHKnownHosts = knownHosts

			runner := NewSSHRunner(&cfg)
			runner.port = server.port()

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

func TestStartContainerSendsDockerStart(t *testing.T) {
	hostKey := newHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := writeTestKey(t, dir)
	server := startTestSSHServer(t, hostKey, publicKey, "")

	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Server.ContainerName = "mc-survival"

	runner := NewSSHRunner(&cfg)
	runner.port = server.port()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := runner.StartContainer(ctx); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if got := server.lastCommand(); got != "docker start mc-survival" {
		t.Errorf("sent %q", got)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	hostKey := newHostKey(t)
	dir := t.TempDir()
	keyPath, _ := writeTestKey(t, dir)
	// The server accepts a different key than the one the runner holds.
	_, otherPublic := writeTestKey(t, t.TempDir())
	server := startTestSSHServer(t, hostKey, otherPublic, "")

	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")

	runner := NewSSHRunner(&cfg)
	runner.port = server.port()

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

	_, err = loadPrivateKey(path)
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
	path, _ := writeTestKey(t, dir)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadPrivateKey(path)
	if err == nil {
		t.Fatal("a world readable key must be refused")
	}
	if !strings.Contains(err.Error(), "readable by other users") {
		t.Errorf("got: %v", err)
	}
}

func TestMissingKeyExplainsHowToCreateOne(t *testing.T) {
	_, err := loadPrivateKey(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing key must be an error")
	}
	if !strings.Contains(err.Error(), "ssh-keygen") {
		t.Errorf("the error should say how to create one, got: %v", err)
	}
}

func TestAuthorizedKeyEntry(t *testing.T) {
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample"

	restricted := authorizedKeyEntry(pub, "minecraft", true)
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

	plain := authorizedKeyEntry(pub, "minecraft", false)
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
	hostKey := newHostKey(t)
	dir := t.TempDir()
	keyPath, publicKey := writeTestKey(t, dir)
	server := startTestSSHServer(t, hostKey, publicKey, "correct-horse")

	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.SSHUser = "tester"
	cfg.Server.SSHKeyPath = keyPath
	cfg.Server.SSHStrictHostKey = "accept-new"
	cfg.Server.SSHKnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Server.ContainerName = "minecraft"

	// Pre-trust the host key so the prompt is never reached.
	line := knownhosts.Line([]string{knownhosts.Normalize(server.addr)}, hostKey.PublicKey())
	if err := os.WriteFile(cfg.Server.SSHKnownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := NewSSHRunner(&cfg)
	runner.port = server.port()

	entry := authorizedKeyEntry("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample", "minecraft", true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p := newPrompterFrom(strings.NewReader(""))
	session, err := DialServerSession(ctx, runner, "correct-horse", p)
	if err != nil {
		t.Fatalf("DialServerSession: %v", err)
	}
	defer session.Close()

	if _, err := appendAuthorizedKey(session, entry); err != nil {
		t.Fatalf("appendAuthorizedKey: %v", err)
	}

	command := server.lastCommand()
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

var errKeyRefused = errors.New("key refused")
