package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/boot"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/duckdns"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sleep"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/update"
)

// Everything the service does: listen, hand each connection to the handler,
// and keep DuckDNS, the assets and the sleep monitor going alongside.
func Run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	waker := boot.NewWaker(cfg)
	handler := NewHandler(cfg, waker)

	address := net.JoinHostPort(cfg.Watcher.ListenAddress, strconv.Itoa(cfg.Watcher.ListenPort))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot listen on %s: %v\n", address, err)
		return 1
	}

	logStartup(cfg)

	var tasks sync.WaitGroup
	if cfg.DuckDNS.Enabled {
		logging.Infof("DuckDNS updater enabled for %s.duckdns.org (every %dh)",
			cfg.DuckDNS.Domain, cfg.DuckDNS.UpdateIntervalHours)
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			duckdns.RunDuckDNSUpdater(ctx, cfg)
		}()
	}

	if cfg.Sleep.Enabled {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			sleep.RunSleepMonitor(ctx, cfg, waker)
		}()
	}

	tasks.Add(1)
	go func() {
		defer tasks.Done()
		handler.KeepAssetsFresh(ctx)
	}()

	// Unblocks the Accept call below when a signal arrives.
	go func() {
		<-ctx.Done()
		logging.Infof("Shutting down...")
		listener.Close()
	}()

	var conns sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			logging.Warnf("Accept failed: %v", err)
			continue
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			handler.Handle(ctx, conn)
		}()
	}

	conns.Wait()
	tasks.Wait()
	logging.Infof("Proxy stopped")
	return 0
}

func logStartup(cfg *config.Config) {
	logging.Infof("Minecraft Wake-on-Demand Proxy %s listening on %s:%d",
		update.Current, cfg.Watcher.ListenAddress, cfg.Watcher.ListenPort)
	logging.Infof("Server: %s (%s) port %d, container '%s'",
		cfg.Server.IP, cfg.Server.MAC, cfg.Server.MCPort, cfg.Server.ContainerName)
	logging.Infof("WoL mode: %s", cfg.WoL.Mode)
	if cfg.Sleep.Enabled {
		logging.Infof("Auto-sleep: %s after %ds without players", cfg.Sleep.Action, cfg.Sleep.IdleAfter)
	}

	if !cfg.Transfer.Enabled {
		logging.Infof("Proxy mode: full connection forwarding")
		return
	}
	logging.Infof("Transfer mode: %s:%d", cfg.Transfer.Host, cfg.Transfer.Port)

	networks := "any private IP"
	if nets := cfg.ParsedLocalNetworks(); len(nets) > 0 {
		parts := make([]string, 0, len(nets))
		for _, n := range nets {
			parts = append(parts, n.String())
		}
		networks = strings.Join(parts, ", ")
	}
	logging.Infof("Local players are transferred to %s:%d instead (local networks: %s)",
		cfg.Server.IP, cfg.Server.MCPort, networks)
}
