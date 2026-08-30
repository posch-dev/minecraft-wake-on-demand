package testsupport

import (
	"path/filepath"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// A config that points at a port nothing listens on, which is what a sleeping
// server looks like from here.
//
// The path lands in the test's own directory on purpose: without one, the
// learned version cache is written next to the test binary and every test in
// the package reads what an earlier one learned.
func SleepingConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "config.yml")
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = 1
	cfg.Server.SSHUser = "nobody"
	cfg.WoL.BroadcastAddress = "255.255.255.255"
	return &cfg
}
