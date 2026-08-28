package main

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
)

// Set via ldflags in the release build.
var version = "dev"

func main() {
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "", "run":
		os.Exit(runProxy())
	case "check", "init", "setup-ssh", "config", "edit", "settings", "update", "get-server-icon", "learn-server-icon":
		// These print a laid out report, so log lines go to stderr instead of
		// interleaving with it.
		log.out = os.Stderr
		switch command {
		case "check":
			os.Exit(runCheck())
		case "init":
			os.Exit(runInit())
		case "setup-ssh":
			os.Exit(runSetupSSH())
		case "update":
			os.Exit(runUpdate())
		case "get-server-icon", "learn-server-icon":
			os.Exit(runGetServerIcon())
		default:
			os.Exit(runConfigEdit())
		}
	case "version", "--version", "-v":
		fmt.Printf("mc-wol-proxy %s\n", version)
	case "help", "--help", "-h":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n\n", command)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprint(w, `mc-wol-proxy, Minecraft Wake-on-Demand watcher

Usage:
  mc-wol-proxy              start the watcher, the same as "run"
  mc-wol-proxy init         answer a few questions and write config.yml
  mc-wol-proxy config       change the configuration, guided
  mc-wol-proxy setup-ssh    create the SSH key and install it on the server
  mc-wol-proxy check        test the setup and say what is missing
  mc-wol-proxy update       install a newer release, after asking
  mc-wol-proxy get-server-icon
                            copy the running server's icon into assets/
  mc-wol-proxy version      print the version
  mc-wol-proxy help         print this text

"edit" and "settings" do the same as "config".

Setting up from scratch is init, then setup-ssh, then check. When init sets
the server up over SSH, setup-ssh is already done.

The config is read from MC_WOL_CONFIG, then config.yml next to the binary
or one directory above it.
`)
}

func runProxy() int {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	waker := NewWaker(cfg)
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
		log.Infof("DuckDNS updater enabled for %s.duckdns.org (every %dh)",
			cfg.DuckDNS.Domain, cfg.DuckDNS.UpdateIntervalHours)
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			runDuckDNSUpdater(ctx, cfg)
		}()
	}

	if cfg.Sleep.Enabled {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			runSleepMonitor(ctx, cfg, waker)
		}()
	}

	// Unblocks the Accept call below when a signal arrives.
	go func() {
		<-ctx.Done()
		log.Infof("Shutting down...")
		listener.Close()
	}()

	var conns sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			log.Warnf("Accept failed: %v", err)
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
	log.Infof("Proxy stopped")
	return 0
}

func logStartup(cfg *Config) {
	log.Infof("Minecraft Wake-on-Demand Proxy %s listening on %s:%d",
		version, cfg.Watcher.ListenAddress, cfg.Watcher.ListenPort)
	log.Infof("Server: %s (%s) port %d, container '%s'",
		cfg.Server.IP, cfg.Server.MAC, cfg.Server.MCPort, cfg.Server.ContainerName)
	log.Infof("WoL mode: %s", cfg.WoL.Mode)
	if cfg.Sleep.Enabled {
		log.Infof("Auto-sleep: %s after %ds without players", cfg.Sleep.Action, cfg.Sleep.IdleAfter)
	}

	if !cfg.Transfer.Enabled {
		log.Infof("Proxy mode: full connection forwarding")
		return
	}
	log.Infof("Transfer mode: %s:%d", cfg.Transfer.Host, cfg.Transfer.Port)

	networks := "any private IP"
	if nets := cfg.ParsedLocalNetworks(); len(nets) > 0 {
		parts := make([]string, 0, len(nets))
		for _, n := range nets {
			parts = append(parts, n.String())
		}
		networks = strings.Join(parts, ", ")
	}
	log.Infof("Local players are transferred to %s:%d instead (local networks: %s)",
		cfg.Server.IP, cfg.Server.MCPort, networks)
}
