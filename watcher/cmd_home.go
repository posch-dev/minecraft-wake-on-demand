package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/netprobe"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/update"
)

// What running mcwod with no argument does at a terminal. Nothing set up yet
// leads into the wizard, otherwise it asks what you came to do.
func runHome() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("Minecraft Wake-on-Demand")
		fmt.Println("")
		return runInit()
	}

	p := ui.NewPrompter()
	for {
		printHomeStatus(cfg)
		update.PrintUpdateHint(cfg)
		printHomeMenu()

		switch strings.ToLower(strings.TrimSpace(p.Line("Choose", "q"))) {
		case "1":
			runCheck()
		case "2":
			runConfigEdit()
		case "3":
			runWorlds()
		case "4":
			runPlayers()
		case "5":
			runGetServerIcon()
		case "6":
			runUpdate()
		case "q", "quit", "exit", "":
			return 0
		default:
			ui.PrintError("Pick one of the numbers, or q to leave.")
		}

		reloaded, err := config.Load()
		if err != nil {
			return 0
		}
		cfg = reloaded
	}
}

func printHomeMenu() {
	fmt.Println("")
	fmt.Println("  1) Check that everything works")
	fmt.Println("  2) Change settings")
	fmt.Println("  3) Manage worlds")
	fmt.Println("  4) Manage players")
	fmt.Println("  5) Use the picture from your server")
	fmt.Println("  6) Update MCWOD")
	fmt.Println("  q) Quit")
}

func printHomeStatus(cfg *config.Config) {
	fmt.Println("\nMinecraft Wake-on-Demand")
	fmt.Println("")
	fmt.Printf("  Server PC:  %s\n", describeServerState(cfg))
	if cfg.DuckDNS.Enabled {
		fmt.Printf("  Address:    %s:%d\n", cfg.DuckDNSHost(), cfg.Watcher.ListenPort)
	}
	if cfg.Sleep.Enabled {
		fmt.Printf("  Auto-sleep: %s after %d minutes\n", cfg.Sleep.Action, cfg.Sleep.IdleAfter/60)
	}
}

// One quick ping, because a menu that takes seconds to appear feels broken.
func describeServerState(cfg *config.Config) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pinger := &netprobe.Pinger{}
	if !pinger.Ping(ctx, cfg.Server.IP, 1500*time.Millisecond) {
		return "asleep, joining wakes it up"
	}
	return "awake"
}
