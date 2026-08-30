package cli

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/boot"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/netprobe"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
)

func checkServer(c *checker, cfg *config.Config, ctx context.Context) bool {
	c.section("Server PC")
	pinger := &netprobe.Pinger{}
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
	if boot.DialSucceeds(ctx, mcAddress, 3*time.Second) {
		c.ok("Minecraft is listening on %s", mcAddress)
	} else {
		c.info("nothing on %s yet, the container is stopped", mcAddress)
	}
	return true
}

// With the helper installed, check can ask real questions instead of guessing
// from a refused command.
func checkRemoteHelper(c *checker, cfg *config.Config, runner *sshx.SSHRunner, ctx context.Context) {
	out, err := runner.Run(ctx, remote.RemoteVerbHello)
	if err != nil {
		if isAuthFailure(err) {
			c.fail("SSH login as %s failed: %v", cfg.Server.SSHUser, err)
			c.hint("the public key is missing from authorized_keys on the server")
			c.hint("run: mcwod setup-ssh")
			return
		}
		c.fail("the helper did not answer: %v", err)
		c.hint("server.remote_helper is true but %s is missing or not bound to the key", remote.RemoteHelperPathUnix)
		c.hint("run: mcwod setup-ssh")
		return
	}
	c.ok("SSH login as %s works", cfg.Server.SSHUser)

	if answer := strings.TrimSpace(out); answer != remote.RemoteHelperMarker {
		if answer == remote.LegacyHelperMarker {
			c.fail("the server still runs the helper from before the rename")
			c.hint("it works, but the paths and the marker changed with mcwod 2.1")
			c.hint("run: mcwod setup-ssh")
			return
		}
		// An older forced command runs docker start for every word it is sent,
		// so a wrong answer here means sleep would start the container instead.
		c.fail("the helper answered %q instead of %q", logging.Sanitize(answer, 60), remote.RemoteHelperMarker)
		c.hint("an older mcwod line in authorized_keys is still bound to the key")
		c.hint("remove it on the server, then run: mcwod setup-ssh")
		return
	}
	c.ok("helper answered, the key is bound to %s", remote.RemoteHelperPathUnix)

	state, err := remote.RunVerb(ctx, runner, remote.RemoteVerbStatus)
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
func checkWakeOnLANDriver(c *checker, runner *sshx.SSHRunner, ctx context.Context) {
	out, err := remote.RunVerb(ctx, runner, remote.RemoteVerbWoLStatus)
	if err != nil {
		c.info("could not read the Wake-on-LAN setting from the network driver")
		c.hint("check it yourself with: ethtool <interface> | grep Wake-on")
		return
	}
	switch remote.ParseWakeOnLANSetting(out) {
	case remote.WolEnabled:
		c.ok("Wake-on-LAN is armed in the network driver")
	case remote.WolDisabled:
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
func checkPlayerQuery(c *checker, cfg *config.Config, runner *sshx.SSHRunner, ctx context.Context) {
	out, err := remote.RunVerb(ctx, runner, remote.RemoteVerbPlayers)
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
	online, ok := remote.ParsePlayerCount(out)
	if !ok {
		c.warn("could not read a player count from %q", logging.Sanitize(out, 60))
		c.hint("the sleep monitor treats an unreadable answer as 'someone is playing'")
		return
	}
	c.ok("player count works, %d online right now", online)
}

func reportContainerState(c *checker, cfg *config.Config, state string) {
	answer := strings.TrimSpace(state)
	switch {
	case answer == "":
		c.warn("container '%s' did not report a state", cfg.Server.ContainerName)
	case !dockerContainerStates[answer]:
		c.info("the key is restricted, so it started the server instead of answering")
		c.hint("that is the recommended setup, the state below is simply not available")
		c.hint("run setup-ssh again to install the helper, which check can question properly")
	case answer == "running":
		c.ok("container '%s' is running", cfg.Server.ContainerName)
	default:
		c.ok("container '%s' exists, state '%s'", cfg.Server.ContainerName, answer)
	}
}
