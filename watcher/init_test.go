package main

import (
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// Only offered to someone reachable from outside, and off unless asked for.
func TestTransferModeIsNotOfferedWithoutDuckDNS(t *testing.T) {
	cfg := config.Default()
	cfg.DuckDNS.Enabled = false

	askTransferMode(newPrompterFrom(strings.NewReader("y\n25566\n")), &cfg)

	if cfg.Transfer.Enabled {
		t.Error("transfer mode was turned on without anything to forward")
	}
}

func TestTransferModeAsksForTheForwardedPort(t *testing.T) {
	cfg := config.Default()
	cfg.DuckDNS.Enabled = true
	cfg.DuckDNS.Domain = "kicercraft"
	cfg.Server.IP = "192.168.178.176"
	cfg.Server.MCPort = 25565

	askTransferMode(newPrompterFrom(strings.NewReader("y\n25566\n")), &cfg)

	if !cfg.Transfer.Enabled {
		t.Fatal("transfer mode should be on after saying yes")
	}
	if cfg.Transfer.Host != "kicercraft.duckdns.org" {
		t.Errorf("host = %q", cfg.Transfer.Host)
	}
	if cfg.Transfer.Port != 25566 {
		t.Errorf("port = %d", cfg.Transfer.Port)
	}
}

func TestTransferModeStaysOffWhenDeclined(t *testing.T) {
	cfg := config.Default()
	cfg.DuckDNS.Enabled = true
	cfg.DuckDNS.Domain = "kicercraft"

	askTransferMode(newPrompterFrom(strings.NewReader("n\n")), &cfg)

	if cfg.Transfer.Enabled {
		t.Error("transfer mode should stay off")
	}
}
