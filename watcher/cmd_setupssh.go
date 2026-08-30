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
		fmt.Println("\nRun 'mcwod init' first.")
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

	fmt.Println("\nThe watcher can also put the server PC back to sleep once nobody is")
	fmt.Println("playing. That needs a small helper script on the server and, on Linux,")
	fmt.Println("one sudoers line. Without it the key can only start the container.")
	wantSleep := p.yesNo("Allow the watcher to send the server PC to sleep", false)

	sleepAction := ""
	if wantSleep {
		sleepAction = strings.ToLower(strings.TrimSpace(p.validated(
			"Which action, suspend, hibernate or shutdown", "suspend",
			func(v string) error {
				if !contains(installableSleepActions, strings.ToLower(strings.TrimSpace(v))) {
					return fmt.Errorf("pick suspend, hibernate or shutdown")
				}
				return nil
			})))
		if sleepAction == "shutdown" {
			fmt.Println("\n  Note: waking from a full shutdown needs Wake-on-LAN enabled in the")
			fmt.Println("  BIOS for the powered-off state, which not every board supports.")
		}
		cfg.Sleep.Action = sleepAction
	}

	fmt.Printf("\nLogging in as %s@%s to install the key.\n", cfg.Server.SSHUser, cfg.Server.IP)
	fmt.Println("This password is used once and is not stored anywhere.")
	password := p.secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("\nNo password given, nothing was changed.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	session, err := DialServerSession(ctx, NewSSHRunner(cfg), password, p)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	defer session.Close()

	platform := session.Detect()
	fmt.Printf("\nServer PC runs %s.\n", platform.Name())
	if !platform.HasDocker {
		fmt.Println("  Warning: no docker found on the server, so the watcher will not be")
		fmt.Println("  able to start the container. Install Docker there before running check.")
	}

	if platform.Windows {
		fmt.Println(windowsHelperInstructions(cfg, publicKey))
		if wantSleep {
			fmt.Println("Set server.remote_helper: true and the sleep block in config.yml once the")
			fmt.Println("script is in place, then run: mcwod check")
		} else {
			fmt.Println("Then run: mcwod check")
		}
		return 0
	}

	entry := ""
	helperInstalled := false
	if wantSleep {
		if platform.SystemctlPath == "" {
			fmt.Println("\nNo systemctl on the server, so there is no standard way to suspend it.")
			fmt.Println("Set sleep.action: custom and sleep.command in config.yml, then run")
			fmt.Println("setup-ssh again.")
			return 1
		}
		if err := installRemoteHelperUnix(session, cfg); err != nil {
			fmt.Printf("\n%v\n", err)
			fmt.Println("\nNothing was installed. The password may have been wrong, or the")
			fmt.Println("account may not be allowed to use sudo.")
			return 1
		}
		fmt.Printf("Helper installed at %s, owned by root.\n", remoteHelperPathUnix)
		fmt.Printf("Sudoers rule written to %s, checked with visudo first.\n", sudoersPath)
		entry = remoteHelperKeyEntryUnix(publicKey)
		helperInstalled = true
	} else {
		restrict := p.yesNo("Restrict the key so it can only start the container (recommended)", true)
		entry = authorizedKeyEntry(publicKey, cfg.Server.ContainerName, restrict)
	}

	if err := appendAuthorizedKey(session, entry); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	fmt.Println("Key installed in authorized_keys.")

	fmt.Println("\nVerifying the key works...")
	runner := NewSSHRunner(cfg)

	if !helperInstalled {
		if _, err := runner.Run(ctx, "true"); err != nil {
			// A restricted key runs its forced command and refuses everything
			// else, so a rejected command still proves the login succeeded.
			if strings.Contains(err.Error(), "unable to authenticate") {
				fmt.Printf("The key was refused: %v\n", err)
				return 1
			}
			fmt.Println("Login works, the key is restricted to starting the container as intended.")
		} else {
			fmt.Println("Login works.")
		}
		fmt.Println("\nNext step: mcwod check")
		return 0
	}

	out, err := runner.Run(ctx, remoteVerbHello)
	if err != nil {
		fmt.Printf("The key was refused: %v\n", err)
		return 1
	}
	if strings.TrimSpace(out) != remoteHelperMarker {
		fmt.Printf("The helper answered %q instead of %q.\n", sanitizeForLog(out, 80), remoteHelperMarker)
		fmt.Println("An older mcwod line in authorized_keys is still bound to the key.")
		fmt.Println("Remove it and run setup-ssh again.")
		return 1
	}
	fmt.Println("Helper answered, the key can start, stop and sleep the server.")

	fmt.Println("\nPut these lines in config.yml, the watcher needs them to use the helper:")
	fmt.Println("  server:")
	fmt.Println("    remote_helper: true")
	fmt.Println("  sleep:")
	fmt.Println("    enabled: true")
	fmt.Printf("    action: %s\n", sleepAction)
	fmt.Println("\nNext step: mcwod check")
	return 0
}

var installableSleepActions = []string{"suspend", "hibernate", "shutdown"}

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
	block, err := ssh.MarshalPrivateKey(private, "mcwod")
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

// Appended only when absent, so a second run does not duplicate the line.
// An older entry is left alone, which is why hello verifies afterwards.
func appendAuthorizedKey(s *ServerSession, entry string) error {
	fields := strings.Fields(entry)
	if len(fields) < 2 {
		return fmt.Errorf("malformed authorized_keys entry")
	}
	keyBody := fields[len(fields)-2]

	command := fmt.Sprintf(
		"set -e; mkdir -p ~/.ssh; chmod 700 ~/.ssh; touch ~/.ssh/authorized_keys; "+
			"chmod 600 ~/.ssh/authorized_keys; "+
			"grep -qF %s ~/.ssh/authorized_keys || printf '%%s\\n' %s >> ~/.ssh/authorized_keys",
		shellQuote(keyBody), shellQuote(entry))

	if out, err := s.Run(command); err != nil {
		return fmt.Errorf("cannot write authorized_keys: %w: %s", err, sanitizeForLog(out, 200))
	}
	return nil
}

// Everything interpolated here is either generated by us or validated on load,
// so this is a guard against surprises rather than the only line of defence.
func shellSafe(value string) string {
	return strings.ReplaceAll(value, "'", "")
}

// Always asks, whatever ssh_strict_host_key says. Someone is sitting here,
// so the fingerprint gets shown instead of silently trusted.
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
		"no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s mcwod\n\n",
		cfg.Server.ContainerName, publicKey)
	fmt.Println("Then run: mcwod check")
}
