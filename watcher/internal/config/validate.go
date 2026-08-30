package config

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func (c *Config) Validate() error {
	if c.Server.MAC == "" {
		return fmt.Errorf("server.mac is missing, put the MAC address of your server PC there, for example AA:BB:CC:DD:EE:FF")
	}
	if _, err := ParseMAC(c.Server.MAC); err != nil {
		return fmt.Errorf("server.mac %q is not a MAC address, it needs 6 hex pairs like AA:BB:CC:DD:EE:FF", c.Server.MAC)
	}
	if c.Server.IP == "" {
		return fmt.Errorf("server.ip is missing, put the local IP of your server PC there, for example 192.168.1.100")
	}
	if net.ParseIP(c.Server.IP) == nil {
		return fmt.Errorf("server.ip %q is not an IP address", c.Server.IP)
	}
	if c.Server.SSHUser == "" {
		return fmt.Errorf("server.ssh_user is missing, put your login name on the server PC there")
	}
	if c.Server.ContainerName == "" {
		return fmt.Errorf("server.container_name is empty, it has to name the Minecraft container, usually 'minecraft'")
	}
	// The name is pasted into a remote shell command, so it stays inside what
	// Docker accepts as a container name.
	if !ContainerNamePattern.MatchString(c.Server.ContainerName) {
		return fmt.Errorf("server.container_name %q is not a Docker container name, "+
			"use letters, digits, underscore, dot or dash", c.Server.ContainerName)
	}
	if c.Watcher.ListenPort < 1 || c.Watcher.ListenPort > 65535 {
		return fmt.Errorf("watcher.listen_port is %d, it has to be between 1 and 65535", c.Watcher.ListenPort)
	}
	if c.Server.MCPort < 1 || c.Server.MCPort > 65535 {
		return fmt.Errorf("server.mc_port is %d, it has to be between 1 and 65535", c.Server.MCPort)
	}
	if c.Watcher.ListenAddress == "" {
		c.Watcher.ListenAddress = "0.0.0.0"
	}

	c.WoL.Mode = strings.ToLower(strings.TrimSpace(c.WoL.Mode))
	switch c.WoL.Mode {
	case "broadcast":
		if c.WoL.BroadcastAddress == "" {
			c.WoL.BroadcastAddress = "255.255.255.255"
			logging.Warnf("wol.broadcast_address is empty, falling back to 255.255.255.255")
		}
		if net.ParseIP(c.WoL.BroadcastAddress) == nil {
			return fmt.Errorf("wol.broadcast_address %q is not an IP address", c.WoL.BroadcastAddress)
		}
	case "unicast":
	default:
		return fmt.Errorf("wol.mode is %q, it has to be 'broadcast' or 'unicast'", c.WoL.Mode)
	}

	c.applyActiveWorld()

	if err := c.validateSleep(); err != nil {
		return err
	}

	if c.Limits.MaxLogins < 0 {
		return fmt.Errorf("limits.max_logins is %d, use 0 to take the limit from the server", c.Limits.MaxLogins)
	}
	if c.Limits.MaxPerIP < 1 {
		logging.Warnf("limits.max_per_ip is %d, falling back to 8", c.Limits.MaxPerIP)
		c.Limits.MaxPerIP = 8
	}

	c.Server.SSHStrictHostKey = strings.ToLower(strings.TrimSpace(c.Server.SSHStrictHostKey))
	if !slices.Contains(strictHostKeyModes, c.Server.SSHStrictHostKey) {
		logging.Warnf("Invalid server.ssh_strict_host_key %q, falling back to 'accept-new'", c.Server.SSHStrictHostKey)
		c.Server.SSHStrictHostKey = "accept-new"
	}
	if c.Server.SSHStrictHostKey == "no" {
		logging.Warnf("server.ssh_strict_host_key is 'no', any host key is accepted, " +
			"which allows man-in-the-middle attacks on the SSH connection")
	}

	if c.DuckDNS.Enabled {
		c.DuckDNS.Domain = NormalizeDuckDNSDomain(c.DuckDNS.Domain)
		if c.DuckDNS.Domain == "" || c.DuckDNS.Token == "" {
			return fmt.Errorf("duckdns.enabled is true but domain or token is missing, set duckdns.enabled: false if you do not use it")
		}
		if c.DuckDNS.UpdateIntervalHours < 1 {
			return fmt.Errorf("duckdns.update_interval_hours is %d, it has to be at least 1", c.DuckDNS.UpdateIntervalHours)
		}
		if len(c.Watcher.AllowedHostnames) == 0 {
			c.Watcher.AllowedHostnames = StringList{c.DuckDNSHost()}
		}
	}

	if c.Transfer.Enabled {
		if c.Transfer.Host == "" {
			return fmt.Errorf("transfer.enabled is true but transfer.host is missing, put the public hostname players get sent to there")
		}
		if c.Transfer.Port < 1 || c.Transfer.Port > 65535 {
			return fmt.Errorf("transfer.port is %d, it has to be between 1 and 65535", c.Transfer.Port)
		}
	}

	if c.Timeouts.BootTimeout < 1 {
		return fmt.Errorf("timeouts.boot_timeout is %d, it has to be at least 1", c.Timeouts.BootTimeout)
	}
	if c.Timeouts.MCReadyTimeout < 1 {
		return fmt.Errorf("timeouts.mc_ready_timeout is %d, it has to be at least 1", c.Timeouts.MCReadyTimeout)
	}

	if c.MOTD.MaxPlayers < 0 {
		return fmt.Errorf("motd.max_players is %d, it cannot be negative", c.MOTD.MaxPlayers)
	}
	for _, m := range []struct {
		name  string
		value string
	}{
		{"motd.sleeping", c.MOTD.Sleeping},
		{"motd.starting", c.MOTD.Starting},
		{"motd.login_wait", c.MOTD.LoginWait},
		{"motd.server_full", c.MOTD.ServerFull},
		{"motd.live", c.MOTD.Live},
	} {
		if m.value == "" {
			continue
		}
		if !json.Valid([]byte(m.value)) {
			return fmt.Errorf("%s is not valid JSON, it has to look like %s", m.name, DefaultMOTDSleeping)
		}
	}
	return nil
}

func (c *Config) validateSleep() error {
	if !c.Sleep.Enabled {
		return nil
	}
	if !c.Server.RemoteHelper {
		return fmt.Errorf("sleep.enabled is true but server.remote_helper is false, " +
			"run 'mcwod setup-ssh' to install the helper the watcher needs to send the PC to sleep")
	}
	c.Sleep.Action = strings.ToLower(strings.TrimSpace(c.Sleep.Action))
	if !slices.Contains(SleepActions, c.Sleep.Action) {
		return fmt.Errorf("sleep.action is %q, it has to be one of %s",
			c.Sleep.Action, strings.Join(SleepActions, ", "))
	}
	if c.Sleep.Action == "custom" && strings.TrimSpace(c.Sleep.Command) == "" {
		return fmt.Errorf("sleep.action is 'custom' but sleep.command is empty, put the command to run there")
	}
	for _, f := range []struct {
		name  string
		value *int
		min   int
	}{
		{"sleep.idle_after", &c.Sleep.IdleAfter, 60},
		{"sleep.confirm_delay", &c.Sleep.ConfirmDelay, 10},
		{"sleep.grace_period", &c.Sleep.GracePeriod, 60},
		{"sleep.poll_interval", &c.Sleep.PollInterval, 30},
	} {
		if *f.value < f.min {
			return fmt.Errorf("%s is %d, it has to be at least %d", f.name, *f.value, f.min)
		}
	}
	return nil
}

// The tool was called mc-wol-proxy until 2.1, so its old variables still work.
// Warned about once, because carrying them forever is how names never die.
