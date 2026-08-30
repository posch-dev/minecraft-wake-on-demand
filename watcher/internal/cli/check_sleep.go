package cli

import (
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func checkSleepAction(c *checker, cfg *config.Config) {
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
		c.hint("run: mcwod setup-ssh")
	}

	c.info("the sleep command itself is not run here, that would switch the PC off")
}
