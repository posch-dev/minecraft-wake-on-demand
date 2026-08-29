package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const sshPort = 22

type SSHRunner struct {
	cfg *config.Config

	// Always 22 in production, the tests point it at a local server.
	port int

	// known_hosts is rewritten on accept-new, so one writer at a time.
	knownHostsMu sync.Mutex
}

func NewSSHRunner(cfg *config.Config) *SSHRunner {
	return &SSHRunner{cfg: cfg, port: sshPort}
}

func (r *SSHRunner) Address() string {
	return net.JoinHostPort(r.cfg.Server.IP, strconv.Itoa(r.port))
}

// OpenSSH refuses group or world readable keys and so do we, an unattended
// service has no business reading a key everyone on the box can read.
func loadPrivateKey(path string) (ssh.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no SSH key at %s, create one with: ssh-keygen -t ed25519 -f %s -N \"\"", path, path)
		}
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("SSH key %s is readable by other users (mode %04o), fix it with: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("SSH key %s is protected by a passphrase, "+
				"the watcher runs unattended and cannot type one, use a key without a passphrase", path)
		}
		return nil, fmt.Errorf("cannot read SSH key %s: %w", path, err)
	}
	return signer, nil
}

// Mirrors StrictHostKeyChecking. A changed key is always a hard failure, that
// is the case host key checking exists for.
func (r *SSHRunner) hostKeyCallback() (ssh.HostKeyCallback, error) {
	mode := r.cfg.Server.SSHStrictHostKey
	if mode == "no" {
		return r.acceptAnyHostKey(), nil
	}

	path := r.cfg.ResolvedKnownHostsPath()
	if err := ensureKnownHostsFile(path); err != nil {
		return nil, err
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		// A populated Want list means the host is known under a different key.
		if len(keyErr.Want) > 0 {
			return fmt.Errorf("host key for %s changed, expected %s but got %s. "+
				"either the server was reinstalled, in which case remove its line from %s, "+
				"or someone is intercepting the connection",
				hostname, keyErr.Want[0].Key.Type(), key.Type(), path)
		}
		if mode != "accept-new" {
			return fmt.Errorf("host key for %s is not in %s, fingerprint is %s. "+
				"connect once with ssh to confirm it, or set server.ssh_strict_host_key: accept-new",
				hostname, path, ssh.FingerprintSHA256(key))
		}

		logging.Infof("Learned host key for %s (%s), fingerprint %s",
			hostname, key.Type(), ssh.FingerprintSHA256(key))
		return r.appendKnownHost(path, hostname, key)
	}, nil
}

// Selected by server.ssh_strict_host_key: no. Every accepted key is logged with
// its fingerprint, so at least the change is visible after the fact.
func (r *SSHRunner) acceptAnyHostKey() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		logging.Warnf("Accepting unverified host key for %s (%s), fingerprint %s, "+
			"because server.ssh_strict_host_key is 'no'",
			hostname, key.Type(), ssh.FingerprintSHA256(key))
		return nil
	}
}

func ensureKnownHostsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	return f.Close()
}

func (r *SSHRunner) appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	r.knownHostsMu.Lock()
	defer r.knownHostsMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	defer f.Close()

	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

func (r *SSHRunner) clientConfig() (*ssh.ClientConfig, error) {
	signer, err := loadPrivateKey(r.cfg.ResolvedSSHKeyPath())
	if err != nil {
		return nil, err
	}
	callback, err := r.hostKeyCallback()
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            r.cfg.Server.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: callback,
		Timeout:         10 * time.Second,
	}, nil
}

// Returns the combined output of the remote command.
func (r *SSHRunner) Run(ctx context.Context, command string) (string, error) {
	clientCfg, err := r.clientConfig()
	if err != nil {
		return "", err
	}

	dialer := net.Dialer{Timeout: clientCfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", r.Address())
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", r.Address(), err)
	}

	// The handshake has no context of its own, so the deadline covers it.
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, r.Address(), clientCfg)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("SSH handshake with %s failed: %w", r.Address(), err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	// Clear the handshake deadline, the command gets its own via the context.
	conn.SetDeadline(time.Time{})

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("cannot open SSH session: %w", err)
	}
	defer session.Close()

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := session.CombinedOutput(command)
		done <- result{out, err}
	}()

	select {
	case <-ctx.Done():
		session.Close()
		return "", ctx.Err()
	case res := <-done:
		return strings.TrimSpace(string(res.out)), res.err
	}
}

// With the helper, a bare verb. Without it, the whole command, which is what
// a plain key and the older forced command expect.
func (r *SSHRunner) RunVerb(ctx context.Context, verb string) (string, error) {
	if r.cfg.Server.RemoteHelper {
		return r.Run(ctx, verb)
	}
	command, err := directCommand(r.cfg, verb)
	if err != nil {
		return "", err
	}
	return r.Run(ctx, command)
}

// A restricted key may ignore the command entirely, which is the recommended
// setup, so an empty response is success as long as the exit status is zero.
func (r *SSHRunner) StartContainer(ctx context.Context) error {
	logging.Infof("Starting container %s via SSH", r.cfg.Server.ContainerName)

	out, err := r.RunVerb(ctx, remoteVerbStart)
	if err != nil {
		if out != "" {
			return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 200))
		}
		return err
	}
	logging.Infof("Container started successfully")
	return nil
}

// Used before a hibernate or shutdown so the world is written out. Suspend does
// not need it, the process simply resumes.
func (r *SSHRunner) StopContainer(ctx context.Context) error {
	logging.Infof("Stopping container %s via SSH", r.cfg.Server.ContainerName)

	out, err := r.RunVerb(ctx, remoteVerbStop)
	if err != nil {
		if out != "" {
			return fmt.Errorf("%w: %s", err, logging.Sanitize(out, 200))
		}
		return err
	}
	return nil
}
