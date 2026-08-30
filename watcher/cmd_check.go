package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type checker struct {
	failures int
	warnings int
}

func (c *checker) section(title string) {
	fmt.Printf("\n%s\n", title)
}

func (c *checker) ok(format string, args ...any) {
	fmt.Printf("  ok    %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) fail(format string, args ...any) {
	c.failures++
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) warn(format string, args ...any) {
	c.warnings++
	fmt.Printf("  warn  %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) info(format string, args ...any) {
	fmt.Printf("  ..    %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) hint(format string, args ...any) {
	fmt.Printf("        %s\n", fmt.Sprintf(format, args...))
}

// Walks the setup in the order things depend on each other and stops digging
// once a step fails, so the first FAIL is the one worth fixing.
func runCheck() int {
	cfg, err := LoadConfig()

	c := &checker{}
	fmt.Println("Checking the Minecraft Wake-on-Demand setup")

	c.section("Config")
	if err != nil {
		c.fail("%v", err)
		fmt.Printf("\n%d problem(s) found.\n", c.failures)
		return 1
	}
	c.ok("read from %s", cfg.Path)
	c.ok("server %s (%s), Minecraft port %d, container '%s'",
		cfg.Server.IP, cfg.Server.MAC, cfg.Server.MCPort, cfg.Server.ContainerName)
	if cfg.Transfer.Enabled {
		c.ok("transfer mode to %s:%d", cfg.Transfer.Host, cfg.Transfer.Port)
	} else {
		c.ok("proxy mode, all traffic goes through the watcher")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	checkAssets(c, cfg)
	checkListenPort(c, cfg)
	sshOK := checkSSHKey(c, cfg)
	hostUp := checkServer(c, cfg, ctx)
	if hostUp && sshOK {
		checkSSHLogin(c, cfg, ctx)
	}
	checkSleepAction(c, cfg)
	checkDuckDNS(c, cfg, ctx)

	fmt.Println()
	switch {
	case c.failures > 0:
		fmt.Printf("%d problem(s) found, %d warning(s).\n", c.failures, c.warnings)
		return 1
	case c.warnings > 0:
		fmt.Printf("No problems found, %d warning(s).\n", c.warnings)
	default:
		fmt.Println("Everything looks good.")
	}
	return 0
}

func checkAssets(c *checker, cfg *Config) {
	c.section("MOTD and icon")
	dir := cfg.AssetsDir()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		c.warn("no assets directory at %s", dir)
		c.hint("the MOTD from config.yml is used instead, which is fine")
		return
	}
	c.ok("assets directory %s", dir)

	assets := NewAssets(cfg)
	for _, state := range []string{stateSleeping, stateStarting, stateLive} {
		reportMOTDSource(c, cfg, assets, state)
	}

	if icon := assets.IconSleeping(); icon == "" {
		c.warn("no icon for the sleeping state, the list shows the default block")
	} else {
		c.ok("sleeping icon ready, %d bytes encoded", len(icon))
	}
	if assets.IconLive() == "" {
		c.info("no server-icon-live.png, the running server's own icon is passed through")
	} else {
		c.ok("server-icon-live.png overrides the running server's icon")
	}
}

func reportMOTDSource(c *checker, cfg *Config, assets *Assets, state string) {
	fromFile := map[string]func() string{
		stateSleeping: assets.MOTDSleeping,
		stateStarting: assets.MOTDStarting,
		stateLive:     assets.MOTDLive,
	}[state]()
	fromConfig := map[string]string{
		stateSleeping: cfg.MOTD.Sleeping,
		stateStarting: cfg.MOTD.Starting,
		stateLive:     cfg.MOTD.Live,
	}[state]

	switch {
	case fromFile == "" && state == stateLive:
		c.info("motd-live.json not set, the running server's own MOTD is passed through")
	case fromFile != fromConfig:
		c.ok("motd-%s.json loaded", state)
	default:
		c.info("motd-%s.json not used, falling back to config.yml", state)
	}
}

func checkListenPort(c *checker, cfg *Config) {
	c.section("Listen port")
	address := net.JoinHostPort(cfg.Watcher.ListenAddress, strconv.Itoa(cfg.Watcher.ListenPort))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// An already running watcher is the usual reason and not a problem.
		c.warn("cannot bind %s: %v", address, err)
		c.hint("this is expected when the watcher is already running")
		return
	}
	listener.Close()
	c.ok("%s is free", address)
}

func checkSSHKey(c *checker, cfg *Config) bool {
	c.section("SSH key")
	path := cfg.ResolvedSSHKeyPath()
	signer, err := loadPrivateKey(path)
	if err != nil {
		c.fail("%v", err)
		c.hint("run: mc-wol-proxy setup-ssh")
		return false
	}
	c.ok("%s, type %s", path, signer.PublicKey().Type())
	c.ok("fingerprint %s", ssh.FingerprintSHA256(signer.PublicKey()))

	if cfg.Server.SSHStrictHostKey == "no" {
		c.warn("server.ssh_strict_host_key is 'no', any host key is accepted")
		c.hint("set it to accept-new unless you have a reason not to")
	} else {
		known := cfg.ResolvedKnownHostsPath()
		if err := ensureKnownHostsFile(known); err != nil {
			c.fail("%v", err)
			return false
		}
		c.ok("known_hosts at %s", known)
	}
	return true
}

func checkServer(c *checker, cfg *Config, ctx context.Context) bool {
	c.section("Server PC")
	pinger := &Pinger{}
	c.info("reachability checks use %s", pinger.Mode())

	if !pinger.Ping(ctx, cfg.Server.IP, 2*time.Second) {
		// Asleep is the normal state, so this is not a failure by itself.
		c.info("%s does not answer, the PC is asleep or switched off", cfg.Server.IP)
		c.hint("that is the normal resting state, the watcher wakes it on demand")
		c.hint("to test the rest of this check, wake the PC and run it again")
		return false
	}
	c.ok("%s answers", cfg.Server.IP)

	mcAddress := net.JoinHostPort(cfg.Server.IP, strconv.Itoa(cfg.Server.MCPort))
	if dialSucceeds(ctx, mcAddress, 3*time.Second) {
		c.ok("Minecraft is listening on %s", mcAddress)
	} else {
		c.info("nothing on %s yet, the container is stopped", mcAddress)
	}
	return true
}

func checkSSHLogin(c *checker, cfg *Config, ctx context.Context) {
	c.section("SSH login and container")
	runner := NewSSHRunner(cfg)

	if cfg.Server.RemoteHelper {
		checkRemoteHelper(c, cfg, runner, ctx)
		return
	}

	out, err := runner.RunVerb(ctx, remoteVerbStatus)
	if err != nil {
		if isAuthFailure(err) {
			c.fail("SSH login as %s failed: %v", cfg.Server.SSHUser, err)
			c.hint("the public key is missing from authorized_keys on the server")
			c.hint("run: mc-wol-proxy setup-ssh")
			return
		}
		// A restricted key refuses everything except its forced command, which
		// is the recommended setup and not something to warn about loudly.
		c.ok("SSH login as %s works", cfg.Server.SSHUser)
		c.info("the key did not run the inspect command: %v", err)
		c.hint("expected when the key is restricted to 'docker start', as recommended")
		c.hint("run setup-ssh again to install the helper, which check can question properly")
		return
	}

	c.ok("SSH login as %s works", cfg.Server.SSHUser)
	reportContainerState(c, cfg, out)
}

// With the helper installed, check can ask real questions instead of guessing
// from a refused command.
func checkRemoteHelper(c *checker, cfg *Config, runner *SSHRunner, ctx context.Context) {
	out, err := runner.Run(ctx, remoteVerbHello)
	if err != nil {
		if isAuthFailure(err) {
			c.fail("SSH login as %s failed: %v", cfg.Server.SSHUser, err)
			c.hint("the public key is missing from authorized_keys on the server")
			c.hint("run: mc-wol-proxy setup-ssh")
			return
		}
		c.fail("the helper did not answer: %v", err)
		c.hint("server.remote_helper is true but %s is missing or not bound to the key", remoteHelperPathUnix)
		c.hint("run: mc-wol-proxy setup-ssh")
		return
	}
	c.ok("SSH login as %s works", cfg.Server.SSHUser)

	if strings.TrimSpace(out) != remoteHelperMarker {
		// An older forced command runs docker start for every word it is sent,
		// so a wrong answer here means sleep would start the container instead.
		c.fail("the helper answered %q instead of %q", sanitizeForLog(out, 60), remoteHelperMarker)
		c.hint("an older mc-wol-proxy line in authorized_keys is still bound to the key")
		c.hint("remove it on the server, then run: mc-wol-proxy setup-ssh")
		return
	}
	c.ok("helper answered, the key is bound to %s", remoteHelperPathUnix)

	state, err := runner.RunVerb(ctx, remoteVerbStatus)
	if err != nil {
		c.warn("the helper could not inspect the container: %v", err)
	} else {
		reportContainerState(c, cfg, state)
	}

	checkWakeOnLANDriver(c, runner, ctx)
	checkPlayerQuery(c, cfg, runner, ctx)
}

// A NIC with Wake-on-LAN switched off in the driver is the most common reason
// this whole project does nothing, and nothing else in the setup reveals it.
func checkWakeOnLANDriver(c *checker, runner *SSHRunner, ctx context.Context) {
	out, err := runner.RunVerb(ctx, remoteVerbWoLStatus)
	if err != nil {
		c.info("could not read the Wake-on-LAN setting from the network driver")
		c.hint("check it yourself with: ethtool <interface> | grep Wake-on")
		return
	}
	switch parseWakeOnLANSetting(out) {
	case wolEnabled:
		c.ok("Wake-on-LAN is armed in the network driver")
	case wolDisabled:
		c.fail("Wake-on-LAN is switched off in the network driver")
		c.hint("the magic packet arrives but the card ignores it, so the PC never wakes")
		c.hint("turn it on with: sudo ethtool -s <interface> wol g")
		c.hint("most distributions reset this on reboot, so make it permanent too")
	default:
		c.info("the network driver did not report a Wake-on-LAN setting")
	}
}

// The sleep monitor counts players through this, so a container without RCON
// would leave it unable to tell an empty server from a busy one.
func checkPlayerQuery(c *checker, cfg *Config, runner *SSHRunner, ctx context.Context) {
	out, err := runner.RunVerb(ctx, remoteVerbPlayers)
	if err != nil {
		if !cfg.Sleep.Enabled {
			c.info("the player count is not available: %v", err)
			return
		}
		c.fail("the player count is not available: %v", err)
		c.hint("sleep.enabled needs it to tell whether anyone is still playing")
		c.hint("enable RCON in the container, itzg/minecraft-server does by default")
		return
	}
	online, ok := parsePlayerCount(out)
	if !ok {
		c.warn("could not read a player count from %q", sanitizeForLog(out, 60))
		c.hint("the sleep monitor treats an unreadable answer as 'someone is playing'")
		return
	}
	c.ok("player count works, %d online right now", online)
}

func checkSleepAction(c *checker, cfg *Config) {
	c.section("Sleep")
	if !cfg.Sleep.Enabled {
		c.info("disabled in config.yml, the server PC is never sent to sleep")
		return
	}
	c.ok("enabled, action '%s' after %ds idle", cfg.Sleep.Action, cfg.Sleep.IdleAfter)

	if cfg.Transfer.Enabled {
		c.warn("transfer mode hides player sessions from the watcher")
		c.hint("sessions skip the watcher after the handoff, so it polls over SSH")
		c.hint("every %ds instead of counting the connections it forwards", cfg.Sleep.PollInterval)
		c.hint("if the container runs with AUTOPAUSE, keep poll_interval above its timeout")
	} else {
		c.ok("proxy mode, player sessions are counted as they pass through")
	}
	if cfg.Sleep.GracePeriod < cfg.Timeouts.BootTimeout+cfg.Timeouts.MCReadyTimeout {
		c.warn("sleep.grace_period is shorter than a full boot takes")
		c.hint("the PC could be sent back to sleep before the first player is in")
	}
	if !cfg.Server.RemoteHelper {
		c.fail("server.remote_helper is false, the watcher cannot send the sleep command")
		c.hint("run: mc-wol-proxy setup-ssh")
	}

	c.info("the sleep command itself is not run here, that would switch the PC off")
}

func reportContainerState(c *checker, cfg *Config, state string) {
	switch strings.TrimSpace(state) {
	case "":
		c.warn("container '%s' did not report a state", cfg.Server.ContainerName)
	case "running":
		c.ok("container '%s' is running", cfg.Server.ContainerName)
	default:
		c.ok("container '%s' exists, state '%s'", cfg.Server.ContainerName, sanitizeForLog(state, 32))
	}
}

func isAuthFailure(err error) bool {
	return strings.Contains(err.Error(), "handshake") || strings.Contains(err.Error(), "unable to authenticate")
}

func checkDuckDNS(c *checker, cfg *Config, ctx context.Context) {
	c.section("DuckDNS")
	if !cfg.DuckDNS.Enabled {
		c.info("disabled in config.yml")
		return
	}
	if err := updateDuckDNS(ctx, cfg); err != nil {
		c.fail("update for %s.duckdns.org failed: %v", cfg.DuckDNS.Domain, err)
		c.hint("check duckdns.domain and duckdns.token")
		return
	}
	c.ok("update for %s.duckdns.org accepted", cfg.DuckDNS.Domain)
}
