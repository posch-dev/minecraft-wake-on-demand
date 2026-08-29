package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/yamledit"
)

// Menu driven editing of an existing config.yml, reached as config, edit or
// settings. init writes the file, this changes it afterwards.
type configEditor struct {
	cfg   *config.Config
	doc   *yamledit.Document
	p     *ui.Prompter
	dirty bool
}

func runConfigEdit() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		fmt.Println("\nRun 'mcwod init' first to create one.")
		return 1
	}
	doc, err := yamledit.Load(cfg.Path)
	if err != nil {
		fmt.Printf("%v\n", err)
		return 1
	}

	editor := &configEditor{cfg: cfg, doc: doc, p: ui.NewPrompter()}
	fmt.Printf("Editing %s\n", cfg.Path)
	fmt.Println("Press Enter to keep the value in brackets.")

	for {
		editor.printMenu()
		switch strings.ToLower(strings.TrimSpace(editor.p.Line("Choose", "q"))) {
		case "1":
			editor.editServer()
		case "2":
			editor.editNetwork()
		case "3":
			editor.editDuckDNS()
		case "4":
			editor.editTransfer()
		case "5":
			editor.editSleep()
		case "6":
			editor.showAssets()
		case "7":
			if code := editor.saveAndCheck(); code != 0 {
				return code
			}
		case "8":
			editor.checkForUpdate()
		case "9":
			editor.setUpContainer()
		case "q", "quit", "exit", "":
			return editor.save()
		default:
			fmt.Println("Pick one of the numbers, or q to save and quit.")
		}
	}
}

func (e *configEditor) printMenu() {
	c := e.cfg
	fmt.Println("\n  1) Server PC        " + fmt.Sprintf("%s as %s, container '%s', port %d",
		c.Server.IP, c.Server.SSHUser, c.Server.ContainerName, c.Server.MCPort))
	fmt.Println("  2) Network          " + fmt.Sprintf("listen %s:%d, broadcast %s",
		c.Watcher.ListenAddress, c.Watcher.ListenPort, c.WoL.BroadcastAddress))
	fmt.Println("  3) DuckDNS          " + duckDNSSummary(c))
	fmt.Println("  4) Transfer mode    " + transferSummary(c))
	fmt.Println("  5) Auto-sleep       " + sleepSummary(c))
	fmt.Println("  6) MOTD and icons   " + assetsSummary(c))
	fmt.Println("  7) Run check")
	fmt.Println("  8) Look for a newer version")
	fmt.Println("  9) Set up the Minecraft container on the server")
	fmt.Println("  q) Save and quit")
}

func duckDNSSummary(c *config.Config) string {
	if !c.DuckDNS.Enabled {
		return "off"
	}
	return fmt.Sprintf("%s, every %dh", c.DuckDNSHost(), c.DuckDNS.UpdateIntervalHours)
}

func transferSummary(c *config.Config) string {
	if !c.Transfer.Enabled {
		return "off, everything is proxied through the watcher"
	}
	return fmt.Sprintf("on, players go to %s:%d", c.Transfer.Host, c.Transfer.Port)
}

func sleepSummary(c *config.Config) string {
	if !c.Sleep.Enabled {
		return "off, the server PC is never sent to sleep"
	}
	return fmt.Sprintf("%s after %ds without players", c.Sleep.Action, c.Sleep.IdleAfter)
}

func assetsSummary(c *config.Config) string {
	dir := c.AssetsDir()
	overrides := []string{}
	for _, name := range []string{
		"motd-sleeping.json", "motd-starting.json", "motd-live.json",
		"server-icon.png", "server-icon-sleeping.png", "server-icon-starting.png", "server-icon-live.png",
	} {
		if _, err := os.Stat(dir + string(os.PathSeparator) + name); err == nil {
			overrides = append(overrides, name)
		}
	}
	if len(overrides) == 0 {
		return "the built-in Z icon and MOTD"
	}
	return fmt.Sprintf("%d file(s) in %s", len(overrides), dir)
}

func (e *configEditor) set(value any, path ...string) {
	if err := e.doc.Set(path, value); err != nil {
		fmt.Printf("  could not set %s: %v\n", strings.Join(path, "."), err)
		return
	}
	e.dirty = true
}

func (e *configEditor) editServer() {
	c := e.cfg
	c.Server.IP = e.p.Validated("IP address or hostname of the server PC", c.Server.IP, validateHostOrIP)
	e.set(c.Server.IP, "server", "ip")

	c.Server.SSHUser = e.p.Validated("Which user the watcher logs in as", c.Server.SSHUser, func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("this cannot be empty")
		}
		return nil
	})
	e.set(c.Server.SSHUser, "server", "ssh_user")

	c.Server.MAC = e.p.Validated("MAC address of the server PC", c.Server.MAC, func(v string) error {
		_, err := config.ParseMAC(v)
		return err
	})
	e.set(c.Server.MAC, "server", "mac")

	c.Server.ContainerName = e.p.Validated("Name of the Minecraft container", c.Server.ContainerName, validateContainerName)
	e.set(c.Server.ContainerName, "server", "container_name")

	c.Server.MCPort = e.p.ValidatedPort("Minecraft port on the server PC", c.Server.MCPort)
	e.set(c.Server.MCPort, "server", "mc_port")
}

func (e *configEditor) editNetwork() {
	c := e.cfg
	c.Watcher.ListenAddress = e.p.Validated("Address the watcher listens on", c.Watcher.ListenAddress, func(v string) error {
		if v == "0.0.0.0" || v == "::" {
			return nil
		}
		return validateHostOrIP(v)
	})
	e.set(c.Watcher.ListenAddress, "watcher", "listen_address")

	c.Watcher.ListenPort = e.p.ValidatedPort("Port the watcher listens on", c.Watcher.ListenPort)
	e.set(c.Watcher.ListenPort, "watcher", "listen_port")

	c.WoL.BroadcastAddress = e.p.Validated("Broadcast address for the wake packet", c.WoL.BroadcastAddress, func(v string) error {
		return validateHostOrIP(v)
	})
	e.set(c.WoL.BroadcastAddress, "wol", "broadcast_address")

	fmt.Println("\nHostnames players may connect through. Empty means no filtering.")
	fmt.Println("A connection from outside your network using any other name is dropped.")
	current := strings.Join(c.Watcher.AllowedHostnames, ", ")
	answer := e.p.Line("Allowed hostnames, comma separated", current)
	c.Watcher.AllowedHostnames = config.SplitList(answer)
	e.set([]string(c.Watcher.AllowedHostnames), "watcher", "allowed_hostnames")
}

func (e *configEditor) editDuckDNS() {
	c := e.cfg
	c.DuckDNS.Enabled = e.p.YesNo("Use DuckDNS", c.DuckDNS.Enabled)
	e.set(c.DuckDNS.Enabled, "duckdns", "enabled")
	if !c.DuckDNS.Enabled {
		return
	}

	c.DuckDNS.Domain = e.p.Validated("Your DuckDNS address", c.DuckDNSHost(), func(v string) error {
		if config.NormalizeDuckDNSDomain(v) == "" {
			return fmt.Errorf("that is empty, it looks like yourname.duckdns.org")
		}
		return nil
	})
	c.DuckDNS.Domain = config.NormalizeDuckDNSDomain(c.DuckDNS.Domain)
	e.set(c.DuckDNS.Domain, "duckdns", "domain")

	if e.p.YesNo("Replace the DuckDNS token", false) {
		if token := e.p.Secret("DuckDNS token"); token != "" {
			c.DuckDNS.Token = token
			e.set(token, "duckdns", "token")
		}
	}
}

func (e *configEditor) editTransfer() {
	c := e.cfg
	fmt.Println("\nOff proxies everything through the watcher, which always works.")
	fmt.Println("On redirects players to the server after the wake up, which is faster")
	fmt.Println("but needs a second port forwarded and hides sessions from auto-sleep.")

	c.Transfer.Enabled = e.p.YesNo("Enable transfer mode", c.Transfer.Enabled)
	e.set(c.Transfer.Enabled, "transfer", "enabled")
	if !c.Transfer.Enabled {
		return
	}

	fallback := c.Transfer.Host
	if fallback == "" && c.DuckDNS.Enabled {
		fallback = c.DuckDNSHost()
	}
	c.Transfer.Host = e.p.Validated("Public hostname players are sent to", fallback, func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("this cannot be empty")
		}
		return nil
	})
	e.set(c.Transfer.Host, "transfer", "host")

	c.Transfer.Port = e.p.ValidatedPort("Port forwarded straight to the server PC", c.Transfer.Port)
	e.set(c.Transfer.Port, "transfer", "port")

	ui.PrintHint("A server MCWOD set up accepts transfers already. One set up by",
		"hand needs accepts-transfers=true in its server.properties.")
}

func (e *configEditor) editSleep() {
	c := e.cfg
	if !c.Server.RemoteHelper {
		fmt.Println("\nThe helper script is not installed on the server, so the watcher has no")
		fmt.Println("way to send the PC to sleep. Run 'mcwod setup-ssh' first.")
		return
	}

	c.Sleep.Enabled = e.p.YesNo("Send the server PC to sleep when nobody plays", c.Sleep.Enabled)
	e.set(c.Sleep.Enabled, "sleep", "enabled")
	if !c.Sleep.Enabled {
		return
	}

	c.Sleep.Action = strings.ToLower(strings.TrimSpace(e.p.Validated(
		"Which action, suspend, hibernate, shutdown or custom", c.Sleep.Action, func(v string) error {
			if slices.Contains(config.SleepActions, strings.ToLower(strings.TrimSpace(v))) {
				return nil
			}
			return fmt.Errorf("pick one of %s", strings.Join(config.SleepActions, ", "))
		})))
	e.set(c.Sleep.Action, "sleep", "action")

	if c.Sleep.Action == "custom" {
		fmt.Println("\nThe helper script on the server has to be reinstalled for a changed")
		fmt.Println("command to take effect, run setup-ssh again afterwards.")
		c.Sleep.Command = e.p.Line("Command to run on the server", c.Sleep.Command)
		e.set(c.Sleep.Command, "sleep", "command")
	}

	for _, f := range []struct {
		question string
		key      string
		value    *int
		min      int
	}{
		{"Seconds without players before sleeping", "idle_after", &c.Sleep.IdleAfter, 60},
		{"Seconds to wait before the confirming check", "confirm_delay", &c.Sleep.ConfirmDelay, 10},
		{"Seconds after a wake in which sleeping is never allowed", "grace_period", &c.Sleep.GracePeriod, 60},
	} {
		*f.value = e.p.ValidatedInt(f.question, *f.value, f.min)
		e.set(*f.value, "sleep", f.key)
	}

	if c.Transfer.Enabled {
		fmt.Println("\nTransfer mode hides sessions from the watcher, so it polls over SSH.")
		c.Sleep.PollInterval = e.p.ValidatedInt("Seconds between those checks", c.Sleep.PollInterval, 30)
		e.set(c.Sleep.PollInterval, "sleep", "poll_interval")
	}
}

func (e *configEditor) showAssets() {
	dir := e.cfg.AssetsDir()
	fmt.Printf("\nAssets live in %s\n", dir)
	fmt.Println("Examples to copy are in assets/examples/.")
	fmt.Println("\nPut one 64x64 server-icon.png there and it is used for all three states:")
	fmt.Println("plain while the server runs, and dimmed under three blue Z while it")
	fmt.Println("sleeps, the largest turning into a red exclamation mark while it boots.")
	fmt.Println("\nTo replace something outright instead:")
	fmt.Println("  motd-sleeping.json          motd-starting.json          motd-live.json")
	fmt.Println("  server-icon-sleeping.png    server-icon-starting.png    server-icon-live.png")
	fmt.Println("\nWithout any of these the running server's own MOTD is passed through, and")
	fmt.Println("its own icon too if there is no server-icon.png either.")
}

// Needs the password login, the restricted key cannot write files.
func (e *configEditor) setUpContainer() {
	fmt.Printf("\nLogging in as %s@%s.\n", e.cfg.Server.SSHUser, e.cfg.Server.IP)
	fmt.Println("The password is used for this one login and is not stored anywhere.")
	password := e.p.Secret(fmt.Sprintf("Password for %s@%s", e.cfg.Server.SSHUser, e.cfg.Server.IP))
	if password == "" {
		fmt.Println("No password given, nothing was changed.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	session, err := DialServerSession(ctx, NewSSHRunner(e.cfg), password, e.p)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return
	}
	defer session.Close()

	facts := ServerFacts{Platform: session.Detect()}
	discoverContainers(session, &facts)
	if !offerContainerSetup(e.p, session, e.cfg, facts) {
		return
	}
	e.set(e.cfg.Server.ContainerName, "server", "container_name")
	e.set(e.cfg.Server.MCPort, "server", "mc_port")
	e.set(e.cfg.Server.ComposeDir, "server", "compose_dir")
}

// Only ever reports, installing is what the update command is for.
func (e *configEditor) checkForUpdate() {
	fmt.Printf("\nInstalled version: %s\n", version)
	release, err := fetchLatestReleaseNow()
	if err != nil {
		fmt.Printf("Could not reach the release API: %v\n", err)
		return
	}
	fmt.Printf("Latest release:    %s\n", release.Tag)
	if isNewerVersion(release.Tag, version) {
		fmt.Println("\nInstall it with: sudo mcwod update")
		return
	}
	fmt.Println("\nAlready up to date.")
}

func (e *configEditor) saveAndCheck() int {
	if code := e.save(); code != 0 {
		return code
	}
	e.dirty = false
	fmt.Println()
	runCheck()
	return 0
}

// Validated before writing, so a menu session cannot leave a config the watcher
// would refuse to start with.
func (e *configEditor) save() int {
	if !e.dirty {
		fmt.Println("Nothing changed.")
		return 0
	}
	if err := e.cfg.Validate(); err != nil {
		fmt.Printf("\nThe answers do not add up: %v\n", err)
		fmt.Println("Nothing was written, fix it and try again.")
		return 1
	}
	if err := e.doc.Save(); err != nil {
		fmt.Printf("\nCannot write %s: %v\n", e.cfg.Path, err)
		return 1
	}
	fmt.Printf("Saved to %s\n", e.cfg.Path)
	defer printUpdateHint(e.cfg)
	if e.cfg.Sleep.Enabled || e.cfg.Watcher.ListenPort != 0 {
		fmt.Println("Restart the watcher for the changes to take effect:")
		fmt.Println("  sudo systemctl restart mcwod")
	}
	return 0
}
