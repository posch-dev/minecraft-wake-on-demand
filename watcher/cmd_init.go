package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// Lets the tests drive the wizard without a terminal.

// Keeps asking until the answer passes, so a typo does not end the wizard.

// Reads without echoing when stdin is a terminal, so tokens stay off the screen.

func runInit() int {
	target := configTargetPath()
	fmt.Printf("This writes a config file to %s\n", target)
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("\n%s already exists. Delete or rename it first, this command will not overwrite it.\n", target)
		return 1
	}
	fmt.Println("Press Enter to accept the value in brackets.")

	p := ui.NewPrompter()
	cfg := config.Default()

	fmt.Println("\nNothing is set up yet, so let's do that now.")
	ui.PrintHint("You can change all of this later.")

	fmt.Println("")
	ui.PrintHint("Look it up in the network settings on that PC, or in your router.")
	cfg.Server.IP = p.Validated("Enter the IP address of the PC that will run Minecraft (192.168.178.xxx)",
		"", validateHostOrIP)

	cfg.Server.SSHUser = p.Validated("\nWhat is your username on that PC?", currentUserName(), func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("this cannot be empty")
		}
		return nil
	})

	fmt.Println("\nI can log in to that PC once and set everything up for you.")
	ui.PrintHint("Your password is used for this one login and is never saved.")
	provisioned := false
	if p.YesNo("Let me do that?", true) {
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
		fmt.Println("")
		ui.PrintHint("That is the name of its Docker container.")
		cfg.Server.ContainerName = p.Validated("What is your Minecraft server called on that PC?",
			"minecraft", validateContainerName)
	}

	// Only asked when the watcher and the server are in different networks,
	// where there is nothing local to work it out from.
	if cfg.WoL.BroadcastAddress == "" {
		cfg.WoL.BroadcastAddress = guessBroadcast(cfg.Server.IP)
	}
	if cfg.WoL.BroadcastAddress == "255.255.255.255" {
		fmt.Println("\nThat PC does not look like it is in the same network as this one.")
		fmt.Println("Waking it needs the broadcast address of its network.")
		cfg.WoL.BroadcastAddress = p.Validated("Broadcast address", "255.255.255.255", func(v string) error {
			if net.ParseIP(v) == nil {
				return fmt.Errorf("that is not an IP address")
			}
			return nil
		})
	}

	fmt.Println("\nYour home internet address changes every few days, so your friends need")
	fmt.Println("a name that follows it. DuckDNS gives you one for free.")
	ui.PrintHint("Skip this if only people on your own network are going to join.")
	cfg.DuckDNS.Enabled = p.YesNo("Use DuckDNS?", true)
	if cfg.DuckDNS.Enabled {
		cfg.DuckDNS.Domain = p.Validated("Your DuckDNS address", "", func(v string) error {
			if config.NormalizeDuckDNSDomain(v) == "" {
				return fmt.Errorf("that is empty, it looks like yourname.duckdns.org")
			}
			return nil
		})
		cfg.DuckDNS.Domain = config.NormalizeDuckDNSDomain(cfg.DuckDNS.Domain)
		ui.PrintHint("It stays visible here so you can check it, and it goes into",
			"config.yml, which only your user can read.")
		for cfg.DuckDNS.Token == "" && !p.Exhausted {
			cfg.DuckDNS.Token = strings.TrimSpace(p.Line("Your DuckDNS token", ""))
		}
	}

	askTransferMode(p, &cfg)

	if err := cfg.Validate(); err != nil {
		fmt.Printf("\nThe answers do not add up: %v\n", err)
		return 1
	}
	if err := writeConfig(target, &cfg); err != nil {
		fmt.Printf("\nCannot write %s: %v\n", target, err)
		return 1
	}

	fmt.Printf("\nWritten to %s\n", target)
	if cfg.DuckDNS.Enabled {
		fmt.Printf("\nAll set. Your friends connect to %s:%d\n", cfg.DuckDNSHost(), cfg.Watcher.ListenPort)
	} else {
		fmt.Println("\nAll set.")
	}
	fmt.Println("")
	if provisioned {
		fmt.Println("Check that everything works: mcwod check")
		printUpdateHint(&cfg)
		return 0
	}
	fmt.Println("Two things left:")
	fmt.Println("  mcwod setup-ssh   let the watcher reach that PC")
	fmt.Println("  mcwod check       check that everything works")
	printUpdateHint(&cfg)
	return 0
}

// The MAC is the value people are least likely to know, so it is read off the
// network instead of asked for whenever the PC is currently reachable.
func askMAC(p *ui.Prompter, ip string) string {
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

	return p.Validated("MAC address of the server PC", detected, func(v string) error {
		if _, err := config.ParseMAC(v); err != nil {
			return fmt.Errorf("that is not a MAC address, it looks like AA:BB:CC:DD:EE:FF")
		}
		return nil
	})
}

// A /24 covers the overwhelming majority of home networks.
// The watcher sits in the same network as the server, so the mask is right
// here rather than guessed. Assuming /24 breaks silently on a /16 or /22, and
// the only symptom is that waking never works.
func guessBroadcast(serverIP string) string {
	ip := net.ParseIP(serverIP).To4()
	if ip == nil {
		return "255.255.255.255"
	}
	if found := broadcastForIP(ip, localNetworks()); found != "" {
		return found
	}
	return fmt.Sprintf("%d.%d.%d.255", ip[0], ip[1], ip[2])
}

func localNetworks() []*net.IPNet {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	nets := []*net.IPNet{}
	for _, addr := range addrs {
		if network, ok := addr.(*net.IPNet); ok && network.IP.To4() != nil {
			nets = append(nets, network)
		}
	}
	return nets
}

// Host bits all set, which is the broadcast address of that subnet.
func broadcastForIP(ip net.IP, nets []*net.IPNet) string {
	for _, network := range nets {
		if !network.Contains(ip) {
			continue
		}
		mask := network.Mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		if len(mask) != net.IPv4len {
			continue
		}
		base := network.IP.To4()
		broadcast := make(net.IP, net.IPv4len)
		for i := range broadcast {
			broadcast[i] = base[i] | ^mask[i]
		}
		return broadcast.String()
	}
	return ""
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
	if env := os.Getenv("MCWOD_CONFIG"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "config.yml")
	}
	return "config.yml"
}

// The file holds the DuckDNS token, so it is not readable by other users.
func writeConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# Written by 'mcwod init'.\n" +
		"# See config.example.yml in the repository for what every setting does.\n"
	if err := os.WriteFile(path, append([]byte(header), data...), 0o600); err != nil {
		return err
	}
	giveToInvokingUser(path)
	return nil
}

// Under sudo the file would belong to root and the service, which runs as the
// user who called sudo, could not read it.
func giveToInvokingUser(path string) {
	uid, err := strconv.Atoi(os.Getenv("SUDO_UID"))
	if err != nil {
		return
	}
	gid, err := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err != nil {
		return
	}
	if err := os.Chown(path, uid, gid); err != nil {
		logging.Warnf("Cannot hand %s to the user who ran sudo: %v", path, err)
	}
}

// Only worth offering to someone whose friends come in from outside, everyone
// else has nothing to forward and nothing to gain.
func askTransferMode(p *ui.Prompter, cfg *config.Config) {
	if !cfg.DuckDNS.Enabled {
		return
	}

	fmt.Println("\nOnce the server is awake, players can either keep going through this PC")
	fmt.Println("or be sent straight to it. Straight to it is faster and takes the load")
	fmt.Println("off this machine.")
	ui.PrintHint("It needs a second port forwarded to the server PC, and the watcher",
		"then no longer sees who is playing, which auto-sleep relies on.")
	if !p.YesNo("Send players straight to the server?", false) {
		return
	}

	cfg.Transfer.Enabled = true
	cfg.Transfer.Host = cfg.DuckDNSHost()
	fmt.Println("")
	ui.PrintHint("Forward this port on your router to " + cfg.Server.IP + ".")
	cfg.Transfer.Port = p.ValidatedPort("Which port goes straight to the server PC?", cfg.Server.MCPort)
	ui.PrintHint("A server MCWOD set up accepts transfers already. One set up by",
		"hand needs accepts-transfers=true in its server.properties.")
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
