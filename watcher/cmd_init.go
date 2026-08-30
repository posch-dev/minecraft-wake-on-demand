package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type prompter struct {
	in *bufio.Reader
}

func newPrompter() *prompter {
	return &prompter{in: bufio.NewReader(os.Stdin)}
}

// Lets the tests drive the wizard without a terminal.
func newPrompterFrom(r io.Reader) *prompter {
	return &prompter{in: bufio.NewReader(r)}
}

func (p *prompter) line(question, fallback string) string {
	if fallback != "" {
		fmt.Printf("%s [%s]: ", question, fallback)
	} else {
		fmt.Printf("%s: ", question)
	}
	text, err := p.in.ReadString('\n')
	if err != nil && text == "" {
		return fallback
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback
	}
	return text
}

// Keeps asking until the answer passes, so a typo does not end the wizard.
func (p *prompter) validated(question, fallback string, check func(string) error) string {
	for {
		answer := p.line(question, fallback)
		if err := check(answer); err != nil {
			fmt.Printf("  %v\n", err)
			continue
		}
		return answer
	}
}

func (p *prompter) yesNo(question string, fallback bool) bool {
	hint := "y/N"
	if fallback {
		hint = "Y/n"
	}
	for {
		fmt.Printf("%s [%s]: ", question, hint)
		text, _ := p.in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "":
			return fallback
		case "y", "yes", "j", "ja":
			return true
		case "n", "no", "nein":
			return false
		}
	}
}

// Reads without echoing when stdin is a terminal, so tokens stay off the screen.
func (p *prompter) secret(question string) string {
	fmt.Printf("%s: ", question)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		data, err := term.ReadPassword(fd)
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	text, _ := p.in.ReadString('\n')
	return strings.TrimSpace(text)
}

func runInit() int {
	target := configTargetPath()
	fmt.Printf("This writes a config file to %s\n", target)
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("\n%s already exists. Delete or rename it first, this command will not overwrite it.\n", target)
		return 1
	}
	fmt.Println("Press Enter to accept the value in brackets.")

	p := newPrompter()
	cfg := defaultConfig()

	fmt.Println("\n--- Server PC ---")
	cfg.Server.IP = p.validated("IP address or hostname of the server PC", "", validateHostOrIP)
	cfg.Server.SSHUser = p.validated("Which user should the watcher log in as", currentUserName(), func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("this cannot be empty")
		}
		return nil
	})

	fmt.Println("\nThe watcher can log in once with a password and set the rest up itself:")
	fmt.Println("MAC address, broadcast address, the container, Wake-on-LAN in the network")
	fmt.Println("driver and its own SSH key. The password is used once and not stored.")
	provisioned := false
	if p.yesNo("Set the server up over SSH", true) {
		signer, err := ensureKeyPair(cfg.ResolvedSSHKeyPath())
		if err != nil {
			fmt.Printf("\nCannot prepare the SSH key: %v\n", err)
			return 1
		}
		publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		provisioned = provisionServer(ctx, p, &cfg, publicKey)
		cancel()
	}

	if !provisioned {
		cfg.Server.MAC = askMAC(p, cfg.Server.IP)
		cfg.Server.ContainerName = p.validated("Name of the Minecraft container", "minecraft", validateContainerName)
	}

	if cfg.WoL.BroadcastAddress == "" {
		fmt.Println("\n--- Network ---")
		cfg.WoL.BroadcastAddress = p.validated("Broadcast address of your network", guessBroadcast(cfg.Server.IP), func(v string) error {
			if net.ParseIP(v) == nil {
				return fmt.Errorf("that is not an IP address")
			}
			return nil
		})
	}

	fmt.Println("\n--- DuckDNS ---")
	fmt.Println("DuckDNS gives you a hostname that follows your changing public IP.")
	fmt.Println("Skip this if only players on your own network are going to join.")
	cfg.DuckDNS.Enabled = p.yesNo("Use DuckDNS", true)
	if cfg.DuckDNS.Enabled {
		cfg.DuckDNS.Domain = p.validated("DuckDNS subdomain, without .duckdns.org", "", func(v string) error {
			if v == "" {
				return fmt.Errorf("this cannot be empty")
			}
			if strings.Contains(v, ".") {
				return fmt.Errorf("only the subdomain, so 'mine' and not 'mine.duckdns.org'")
			}
			return nil
		})
		for cfg.DuckDNS.Token == "" {
			cfg.DuckDNS.Token = p.secret("DuckDNS token")
		}
	}

	fmt.Println("\n--- Transfer mode ---")
	fmt.Println("Off means all traffic goes through this machine, which always works.")
	fmt.Println("On means players are redirected to the server after the wake up,")
	fmt.Println("which is faster but needs a second port forwarded on your router.")
	cfg.Transfer.Enabled = p.yesNo("Enable transfer mode", false)
	if cfg.Transfer.Enabled {
		fallbackHost := ""
		if cfg.DuckDNS.Enabled {
			fallbackHost = cfg.DuckDNS.Domain + ".duckdns.org"
		}
		cfg.Transfer.Host = p.validated("Public hostname players are sent to", fallbackHost, func(v string) error {
			if v == "" {
				return fmt.Errorf("this cannot be empty")
			}
			return nil
		})
		cfg.Transfer.Port = p.validatedPort("Port forwarded straight to the server PC", 25566)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Printf("\nThe answers do not add up: %v\n", err)
		return 1
	}
	if err := writeConfig(target, &cfg); err != nil {
		fmt.Printf("\nCannot write %s: %v\n", target, err)
		return 1
	}

	fmt.Printf("\nWritten to %s\n", target)
	fmt.Println("\nNext steps:")
	if provisioned {
		fmt.Println("  mc-wol-proxy check           confirm everything is wired up")
		printUpdateHint(&cfg)
		return 0
	}
	fmt.Println("  1. mc-wol-proxy setup-ssh    give the watcher access to the server PC")
	fmt.Println("  2. mc-wol-proxy check        confirm everything is wired up")
	printUpdateHint(&cfg)
	return 0
}

func (p *prompter) validatedPort(question string, fallback int) int {
	answer := p.validated(question, strconv.Itoa(fallback), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("that is not a port number between 1 and 65535")
		}
		return nil
	})
	n, _ := strconv.Atoi(answer)
	return n
}

// The MAC is the value people are least likely to know, so it is read off the
// network instead of asked for whenever the PC is currently reachable.
func askMAC(p *prompter, ip string) string {
	fmt.Printf("Looking up the MAC address of %s...\n", ip)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	detected, err := LookupMAC(ctx, ip)
	if err != nil {
		fmt.Printf("  could not find it: %v\n", err)
		fmt.Println("  switch the PC on and try again, or type the address in by hand")
	} else {
		fmt.Printf("  found %s\n", detected)
	}

	return p.validated("MAC address of the server PC", detected, func(v string) error {
		if _, err := ParseMAC(v); err != nil {
			return fmt.Errorf("that is not a MAC address, it looks like AA:BB:CC:DD:EE:FF")
		}
		return nil
	})
}

// A /24 covers the overwhelming majority of home networks.
func guessBroadcast(serverIP string) string {
	ip := net.ParseIP(serverIP).To4()
	if ip == nil {
		return "255.255.255.255"
	}
	return fmt.Sprintf("%d.%d.%d.255", ip[0], ip[1], ip[2])
}

func currentUserName() string {
	for _, key := range []string{"SUDO_USER", "USER", "USERNAME"} {
		if v := os.Getenv(key); v != "" && v != "root" {
			return v
		}
	}
	return ""
}

func configTargetPath() string {
	if env := os.Getenv("MC_WOL_CONFIG"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "config.yml")
	}
	return "config.yml"
}

// The file holds the DuckDNS token, so it is not readable by other users.
func writeConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# Written by 'mc-wol-proxy init'.\n" +
		"# See config.example.yml in the repository for what every setting does.\n"
	return os.WriteFile(path, append([]byte(header), data...), 0o600)
}

// A hostname is accepted, but WoL and the MAC lookup need the address, so it is
// resolved once here rather than failing later.
func validateHostOrIP(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("this cannot be empty")
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	if _, err := net.LookupHost(value); err != nil {
		return fmt.Errorf("%q is neither an IP address nor a name that resolves", value)
	}
	return nil
}
