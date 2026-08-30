package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/update"
)

type checker struct {
	failures int
	warnings int
}

// Walks the setup in the order things depend on each other and stops digging
// once a step fails, so the first FAIL is the one worth fixing.
func RunCheck() int {
	cfg, err := config.Load()

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
	code := 0
	switch {
	case c.failures > 0:
		fmt.Printf("%d problem(s) found, %d warning(s).\n", c.failures, c.warnings)
		code = 1
	case c.warnings > 0:
		fmt.Printf("No problems found, %d warning(s).\n", c.warnings)
	default:
		fmt.Println("Everything looks good.")
	}
	update.PrintUpdateHint(cfg)
	return code
}

// Everything docker inspect can report for .State.Status. Anything else came
// from a forced command that ignored what it was sent.
var dockerContainerStates = map[string]bool{
	"created": true, "restarting": true, "running": true,
	"removing": true, "paused": true, "exited": true, "dead": true,
}
