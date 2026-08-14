package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func runSetupSSH() int {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		fmt.Println("\nRun 'mc-wol-proxy init' first.")
		return 1
	}

	keyPath := cfg.ResolvedSSHKeyPath()
	fmt.Printf("Setting up SSH access to %s@%s\n", cfg.Server.SSHUser, cfg.Server.IP)

	signer, err := ensureKeyPair(keyPath)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	fmt.Printf("Public key: %s\n", ssh.FingerprintSHA256(signer.PublicKey()))

	p := newPrompter()

	// Windows servers run a different shell and need the full docker path, so
	// they get the line to paste rather than a half working automation.
	if !p.yesNo("\nIs the server PC running Linux", true) {
		printManualInstructions(cfg, publicKey)
		return 0
	}

	restrict := p.yesNo("Restrict the key so it can only start the container (recommended)", true)
	entry := authorizedKeyEntry(publicKey, cfg.Server.ContainerName, restrict)

	fmt.Printf("\nLogging in as %s@%s to install the key.\n", cfg.Server.SSHUser, cfg.Server.IP)
	fmt.Println("This password is used once and is not stored anywhere.")
	password := p.secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("\nNo password given, nothing was changed.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := installAuthorizedKey(ctx, cfg, password, entry, p); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	fmt.Println("Key installed in authorized_keys.")

	fmt.Println("\nVerifying the key works...")
	runner := NewSSHRunner(cfg)
	if _, err := runner.Run(ctx, "true"); err != nil {
		if restrict {
			// A restricted key runs docker start and refuses anything else,
			// so a rejected command still proves the login succeeded.
			if strings.Contains(err.Error(), "unable to authenticate") {
				fmt.Printf("The key was refused: %v\n", err)
				return 1
			}
			fmt.Println("Login works, the key is restricted to starting the container as intended.")
			return 0
		}
		fmt.Printf("The key was refused: %v\n", err)
		return 1
	}
	fmt.Println("Login works.")
	fmt.Println("\nNext step: mc-wol-proxy check")
	return 0
}

// The restricted form is what SECURITY.md recommends: even a leaked key can
// then only start the one container.
func authorizedKeyEntry(publicKey, containerName string, restrict bool) string {
	if !restrict {
		return publicKey + " mc-wol-proxy"
	}
	return fmt.Sprintf("command=\"docker start %s\",no-port-forwarding,no-X11-forwarding,"+
		"no-agent-forwarding,no-pty %s mc-wol-proxy", containerName, publicKey)
}

// Generates a key without a passphrase, because the watcher runs unattended and
// has no way to type one.
func ensureKeyPair(path string) (ssh.Signer, error) {
	if _, err := os.Stat(path); err == nil {
		signer, err := loadPrivateKey(path)
		if err != nil {
			return nil, err
		}
		fmt.Printf("Using the existing key at %s\n", path)
		return signer, nil
	}

	fmt.Printf("Creating a new key at %s\n", path)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(private, "mc-wol-proxy")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}

	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return nil, err
	}
	pubLine := append(ssh.MarshalAuthorizedKey(sshPublic)[:0:0], ssh.MarshalAuthorizedKey(sshPublic)...)
	if err := os.WriteFile(path+".pub", pubLine, 0o644); err != nil {
		return nil, fmt.Errorf("cannot write %s.pub: %w", path, err)
	}
	return ssh.NewSignerFromKey(private)
}

func installAuthorizedKey(ctx context.Context, cfg *Config, password, entry string, p *prompter) error {
	return installAuthorizedKeyVia(ctx, NewSSHRunner(cfg), password, entry, p)
}

// Split out so the tests can point the runner at a local server.
func installAuthorizedKeyVia(ctx context.Context, runner *SSHRunner, password, entry string, p *prompter) error {
	cfg := runner.cfg
	callback, err := interactiveHostKeyCallback(runner, p)
	if err != nil {
		return err
	}

	clientCfg := &ssh.ClientConfig{
		User: cfg.Server.SSHUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: callback,
		Timeout:         15 * time.Second,
	}

	dialer := net.Dialer{Timeout: clientCfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", runner.Address())
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", runner.Address(), err)
	}
	conn.SetDeadline(time.Now().Add(45 * time.Second))

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, runner.Address(), clientCfg)
	if err != nil {
		conn.Close()
		return fmt.Errorf("login as %s failed: %w", cfg.Server.SSHUser, err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	conn.SetDeadline(time.Time{})

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	// The key is only appended when it is not already there, so running this
	// twice does not pile up duplicate lines.
	command := fmt.Sprintf(
		"set -e; mkdir -p ~/.ssh; chmod 700 ~/.ssh; touch ~/.ssh/authorized_keys; "+
			"chmod 600 ~/.ssh/authorized_keys; "+
			"grep -qF '%s' ~/.ssh/authorized_keys || printf '%%s\\n' '%s' >> ~/.ssh/authorized_keys",
		shellSafe(strings.Fields(entry)[len(strings.Fields(entry))-2]), shellSafe(entry))

	if out, err := session.CombinedOutput(command); err != nil {
		return fmt.Errorf("cannot write authorized_keys: %w: %s", err, sanitizeForLog(string(out), 200))
	}
	return nil
}

// Everything interpolated here is either generated by us or validated on load,
// so this is a guard against surprises rather than the only line of defence.
func shellSafe(value string) string {
	return strings.ReplaceAll(value, "'", "")
}

// Always asks about an unknown host, whatever server.ssh_strict_host_key says.
// This runs with someone sitting in front of it, so the fingerprint gets shown
// and confirmed rather than trusted silently the way the watcher does.
func interactiveHostKeyCallback(runner *SSHRunner, p *prompter) (ssh.HostKeyCallback, error) {
	path := runner.cfg.ResolvedKnownHostsPath()
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
		if len(keyErr.Want) > 0 {
			return fmt.Errorf("host key for %s changed, expected %s but got %s. "+
				"either the server was reinstalled, in which case remove its line from %s, "+
				"or someone is intercepting the connection",
				hostname, keyErr.Want[0].Key.Type(), key.Type(), path)
		}

		fmt.Printf("\nThe server %s presents this host key:\n", hostname)
		fmt.Printf("  type        %s\n", key.Type())
		fmt.Printf("  fingerprint %s\n", ssh.FingerprintSHA256(key))
		if !p.yesNo("Is that the right server", false) {
			return fmt.Errorf("host key rejected")
		}
		return runner.appendKnownHost(path, hostname, key)
	}, nil
}

func printManualInstructions(cfg *Config, publicKey string) {
	fmt.Println("\nOn a Windows server, add this to authorized_keys by hand.")
	fmt.Printf("The file lives in C:\\Users\\%s\\.ssh\\authorized_keys\n\n", cfg.Server.SSHUser)
	fmt.Printf("command=\"C:\\Program Files\\Docker\\Docker\\resources\\bin\\docker.exe start %s\","+
		"no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s mc-wol-proxy\n\n",
		cfg.Server.ContainerName, publicKey)
	fmt.Println("Then run: mc-wol-proxy check")
}
