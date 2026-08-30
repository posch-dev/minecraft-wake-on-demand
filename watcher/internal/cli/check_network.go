package cli

import (
	"context"
	"net"
	"strconv"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/duckdns"
)

func checkListenPort(c *checker, cfg *config.Config) {
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

func checkDuckDNS(c *checker, cfg *config.Config, ctx context.Context) {
	c.section("DuckDNS")
	if !cfg.DuckDNS.Enabled {
		c.info("disabled in config.yml")
		return
	}
	if err := duckdns.UpdateDuckDNS(ctx, cfg); err != nil {
		c.fail("update for %s failed: %v", cfg.DuckDNSHost(), err)
		c.hint("check duckdns.domain and duckdns.token")
		return
	}
	c.ok("update for %s accepted", cfg.DuckDNSHost())
}
