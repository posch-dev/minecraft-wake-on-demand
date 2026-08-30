package testsupport

import (
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// without waiting for a timeout.
// A config that points at a port nothing listens on, which is what a sleeping
// server looks like from here.
func SleepingConfig() *config.Config {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = 1
	cfg.Server.SSHUser = "nobody"
	cfg.WoL.BroadcastAddress = "255.255.255.255"
	return &cfg
}
