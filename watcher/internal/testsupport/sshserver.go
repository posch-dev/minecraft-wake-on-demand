package testsupport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

var errKeyRefused = errors.New("key refused")

// A real SSH server in the test process, so the host key policy is exercised
// against an actual handshake rather than only read.
type TestSSHServer struct {
	Addr     string
	hostKey  ssh.Signer
	mu       sync.Mutex
	commands []string
	Listener net.Listener
}

func NewHostKey(t *testing.T) ssh.Signer {
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

func WriteTestKey(t *testing.T, dir string) (string, ssh.PublicKey) {
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

func StartTestSSHServer(t *testing.T, hostKey ssh.Signer, accept ssh.PublicKey, password string) *TestSSHServer {
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
	server := &TestSSHServer{Addr: listener.Addr().String(), hostKey: hostKey, Listener: listener}
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

func (s *TestSSHServer) handle(conn net.Conn, cfg *ssh.ServerConfig) {
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

func (s *TestSSHServer) LastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.commands) == 0 {
		return ""
	}
	return s.commands[len(s.commands)-1]
}

func (s *TestSSHServer) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr)
	n, _ := strconv.Atoi(p)
	return n
}
