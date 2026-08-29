package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// One password login that fills in the config, arms the network card and
// installs the key, so the only things asked for are the address and the user.
func provisionServer(ctx context.Context, p *prompter, cfg *config.Config, publicKey string) bool {
	fmt.Printf("\nLogging in as %s@%s.\n", cfg.Server.SSHUser, cfg.Server.IP)
	fmt.Println("The password is used for this one login and is not stored anywhere.")
	password := p.secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("No password given, falling back to the questions.")
		return false
	}

	session, err := DialServerSession(ctx, NewSSHRunner(cfg), password, p)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		fmt.Println("Falling back to the questions.")
		return false
	}
	defer session.Close()

	platform := session.Detect()
	fmt.Printf("\nConnected. The server PC runs %s.\n", platform.Name())

	facts := discoverServer(session)
	applyFacts(p, session, cfg, facts)
	offerWakeOnLANFix(p, session, facts)
	if offerContainerSetup(p, session, cfg, facts) {
		facts.Containers = append(facts.Containers, cfg.Server.ContainerName)
		facts.RCONEnabled = true
	}

	action := offerSleep(p, facts)
	entry := authorizedKeyEntry(publicKey, cfg.Server.ContainerName, cfg.Server.ComposeDir, true)
	if action != "" {
		cfg.Sleep.Action = action
		if platform.Windows {
			fmt.Println(windowsHelperInstructions(cfg, publicKey))
			fmt.Println("Auto-sleep stays off until that script is in place.")
			cfg.Sleep.Enabled = false
		} else if err := installRemoteHelperUnix(session, cfg); err != nil {
			fmt.Printf("\nThe helper could not be installed: %v\n", err)
			fmt.Println("Auto-sleep stays off, the key will only start the container.")
		} else {
			fmt.Printf("Helper installed at %s, owned by root.\n", remoteHelperPathUnix)
			cfg.Server.RemoteHelper = true
			cfg.Sleep.Enabled = true
			entry = remoteHelperKeyEntryUnix(publicKey)
		}
	}

	if platform.Windows {
		fmt.Println("\nWindows OpenSSH needs the key placed by hand:")
		fmt.Println(windowsAuthorizedKeysNote(cfg.Server.SSHUser))
		fmt.Printf("%s\n\n", entry)
		return true
	}

	status, err := appendAuthorizedKey(session, entry)
	if err != nil {
		fmt.Printf("\nThe key could not be installed: %v\n", err)
		return true
	}
	if status == "replaced" {
		fmt.Println("SSH key entry in authorized_keys replaced, the old one was out of date.")
	} else {
		fmt.Println("SSH key installed in authorized_keys.")
	}
	return true
}

// Everything found is shown and confirmed, never applied behind the user's back.
func applyFacts(p *prompter, session *ServerSession, cfg *config.Config, facts ServerFacts) {
	fmt.Println("\n--- What the server told us ---")

	if facts.MAC != "" {
		fmt.Printf("MAC address of %s: %s\n", facts.Interface, facts.MAC)
		cfg.Server.MAC = facts.MAC
	} else {
		fmt.Println("Could not read the MAC address off the server.")
		cfg.Server.MAC = askMAC(p, cfg.Server.IP)
	}

	if facts.Broadcast != "" {
		fmt.Printf("Broadcast address: %s\n", facts.Broadcast)
		cfg.WoL.BroadcastAddress = facts.Broadcast
	}

	cfg.Server.ContainerName = pickContainer(p, facts)
	// Port and RCON only make sense once the container is settled.
	if facts.Platform.HasDocker {
		facts.MCPort, facts.RCONEnabled = inspectContainer(session, cfg.Server.ContainerName)
	}

	if facts.MCPort > 0 && facts.MCPort != cfg.Server.MCPort {
		fmt.Printf("The container publishes Minecraft on port %d.\n", facts.MCPort)
		cfg.Server.MCPort = facts.MCPort
	}
	if len(facts.Containers) > 0 && !facts.RCONEnabled {
		fmt.Println("\nRCON looks switched off in that container. The watcher needs it to")
		fmt.Println("count players, so auto-sleep will not be able to tell an empty server")
		fmt.Println("from a busy one. Set ENABLE_RCON=true in docker-compose.yml.")
	}
	reportDockerState(facts)
}

// Docker Desktop only runs while somebody is logged in, so a resumed PC can
// come back without its container. Nothing here fixes that, but it explains it.
func reportDockerState(facts ServerFacts) {
	if facts.Platform.HasDocker {
		if facts.Platform.Windows {
			fmt.Println("\nDocker answers. Note that Docker Desktop only runs while a user is")
			fmt.Println("logged in on the server, so after a suspend the container may not come")
			fmt.Println("back until someone signs in. Set it to start with Windows, or run the")
			fmt.Println("server in WSL, if the PC is meant to be headless.")
		}
		return
	}
	if facts.Platform.Windows {
		fmt.Println("\nDocker did not answer on the server. Install Docker Desktop and make")
		fmt.Println("sure it is running, it has to be started before the watcher can reach it.")
		return
	}
	fmt.Println("\nNo docker on the server. Install it before running check.")
}

func pickContainer(p *prompter, facts ServerFacts) string {
	if len(facts.Containers) == 0 {
		return p.validated("Name of the Minecraft container", "minecraft", validateContainerName)
	}
	if len(facts.Containers) == 1 {
		fmt.Printf("One container on the server: %s\n", facts.Containers[0])
		return facts.Containers[0]
	}

	fmt.Println("\nContainers on the server:")
	for i, name := range facts.Containers {
		fmt.Printf("  %d) %s\n", i+1, name)
	}
	return p.validated("Which one runs Minecraft", facts.Containers[0], func(v string) error {
		if slices.Contains(facts.Containers, strings.TrimSpace(v)) {
			return nil
		}
		return validateContainerName(v)
	})
}

func validateContainerName(v string) error {
	if !config.ContainerNamePattern.MatchString(strings.TrimSpace(v)) {
		return fmt.Errorf("use letters, digits, underscore, dot or dash")
	}
	return nil
}

// The single most common reason this project appears to do nothing at all.
func offerWakeOnLANFix(p *prompter, session *ServerSession, facts ServerFacts) {
	switch facts.WakeOnLAN {
	case wolEnabled:
		fmt.Println("Wake-on-LAN is already armed in the network driver.")
		return
	case wolUnknown:
		fmt.Println("\nCould not read the Wake-on-LAN setting from the driver.")
		fmt.Println("If waking never works, check it with: ethtool " + facts.Interface)
		return
	}

	fmt.Println("\nWake-on-LAN is switched OFF in the network driver.")
	fmt.Println("The magic packet would arrive and the card would ignore it, so nothing")
	fmt.Println("in this project can work until it is on.")
	if !p.yesNo("Turn it on now, and again on every boot", true) {
		fmt.Printf("Left alone. Turn it on with: sudo ethtool -s %s wol g\n", facts.Interface)
		return
	}

	if err := enableWakeOnLAN(session, facts.Interface); err != nil {
		fmt.Printf("  did not work: %v\n", err)
		fmt.Printf("  do it by hand with: sudo ethtool -s %s wol g\n", facts.Interface)
		return
	}
	fmt.Println("  armed, and a systemd unit re-arms it after every boot.")
}

// Returns the chosen action, empty when the watcher should not sleep the PC.
func offerSleep(p *prompter, facts ServerFacts) string {
	fmt.Println("\nWhen the last player leaves, Minecraft pauses itself. Your PC stays on though.")
	fmt.Println("")
	if !p.yesNo("Should the PC switch itself off as well when nobody plays?", false) {
		return ""
	}
	printHint("Your PC's own power settings cannot do this reliably: they only watch",
		"for mouse and keyboard, not for players, so they tend to switch off in",
		"the middle of a game. Turn any automatic sleep off there.")

	fmt.Println("\nHow should it switch off?")
	for i, choice := range sleepChoices {
		fmt.Printf("  %d) %-10s %s\n", i+1, choice.label, hint(choice.what))
	}
	answer := p.validated("Pick one", "1", func(v string) error {
		if _, ok := sleepActionByChoice(v); ok {
			return nil
		}
		return fmt.Errorf("pick a number from 1 to %d", len(sleepChoices))
	})
	action, _ := sleepActionByChoice(answer)

	if action == "suspend" && !facts.CanSuspend && facts.CanHibernate {
		printWarning("That PC cannot sleep to RAM, only hibernate. Using hibernate.")
		action = "hibernate"
	}
	if action == "suspend" && facts.Platform.Windows {
		printWarning("On Windows, sleep over a remote connection is unreliable.")
		printHint("If waking misbehaves, switch to hibernate in mcwod config.")
	}
	if !facts.Platform.Windows && facts.Platform.SystemctlPath == "" {
		printWarning("That PC has no systemctl, so there is no standard way to switch it off.")
		printHint("Set sleep.action to custom and sleep.command in config.yml instead.")
		return ""
	}
	return action
}

// Named the way somebody would say it, not the way systemd spells it.
var sleepChoices = []struct{ label, action, what string }{
	{"Sleep", "suspend", "recommended, wakes fastest and most reliably"},
	{"Hibernate", "hibernate", "uses no power at all, wakes slower, not recommended"},
	{"Shut down", "shutdown", "needs Wake-on-LAN set in the BIOS, not recommended"},
}

// Takes the number or the word, so either way of answering works.
func sleepActionByChoice(answer string) (string, bool) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	if index, err := strconv.Atoi(answer); err == nil {
		if index >= 1 && index <= len(sleepChoices) {
			return sleepChoices[index-1].action, true
		}
		return "", false
	}
	if slices.Contains(installableSleepActions, answer) {
		return answer, true
	}
	return "", false
}
