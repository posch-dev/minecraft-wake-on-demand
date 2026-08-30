package main

import (
	"context"
	"fmt"
	"strings"
)

// One password login that fills in the config, arms the network card and
// installs the key, so the only things asked for are the address and the user.
func provisionServer(ctx context.Context, p *prompter, cfg *Config, publicKey string) bool {
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
	entry := authorizedKeyEntry(publicKey, cfg.Server.ContainerName, true)
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

	if err := appendAuthorizedKey(session, entry); err != nil {
		fmt.Printf("\nThe key could not be installed: %v\n", err)
		return true
	}
	fmt.Println("SSH key installed in authorized_keys.")
	return true
}

// Everything found is shown and confirmed, never applied behind the user's back.
func applyFacts(p *prompter, session *ServerSession, cfg *Config, facts ServerFacts) {
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
		if contains(facts.Containers, strings.TrimSpace(v)) {
			return nil
		}
		return validateContainerName(v)
	})
}

func validateContainerName(v string) error {
	if !containerNamePattern.MatchString(strings.TrimSpace(v)) {
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
	fmt.Println("\n--- Auto-sleep ---")
	fmt.Println("The watcher can send the PC back to sleep once nobody is playing.")
	fmt.Println("That needs a helper script on the server and one sudoers line.")
	if !p.yesNo("Set that up", false) {
		return ""
	}

	fallback := "suspend"
	if !facts.CanSuspend && facts.CanHibernate {
		fmt.Println("The kernel does not offer suspend to RAM, hibernate does work.")
		fallback = "hibernate"
	}
	action := strings.ToLower(strings.TrimSpace(p.validated(
		"Which action, suspend, hibernate or shutdown", fallback, func(v string) error {
			if contains(installableSleepActions, strings.ToLower(strings.TrimSpace(v))) {
				return nil
			}
			return fmt.Errorf("pick suspend, hibernate or shutdown")
		})))

	if action == "shutdown" {
		fmt.Println("  Waking from a full shutdown needs Wake-on-LAN enabled in the BIOS")
		fmt.Println("  for the powered-off state, which not every board supports.")
	}
	if action == "suspend" && facts.Platform.Windows {
		fmt.Println("  On Windows, suspend over SSH is unreliable. Hibernate is the safer")
		fmt.Println("  choice, change sleep.action in config.yml if waking misbehaves.")
	}
	if !facts.Platform.Windows && facts.Platform.SystemctlPath == "" {
		fmt.Println("  No systemctl on the server, so there is no standard sleep command.")
		fmt.Println("  Set sleep.action: custom and sleep.command in config.yml instead.")
		return ""
	}
	return action
}
