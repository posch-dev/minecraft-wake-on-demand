package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// A config written by init has to be readable by the watcher, otherwise the
// wizard hands people a file that fails on the next start.
func TestWrittenConfigLoadsBack(t *testing.T) {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "192.168.1.100"
	cfg.Server.SSHUser = "someone"
	cfg.Server.ContainerName = "minecraft"
	cfg.WoL.BroadcastAddress = "192.168.1.255"
	cfg.DuckDNS.Enabled = true
	cfg.DuckDNS.Domain = "mine"
	cfg.DuckDNS.Token = "secret-token"
	cfg.Transfer.Enabled = true
	cfg.Transfer.Host = "mine.duckdns.org"
	cfg.Transfer.Port = 25566
	cfg.Transfer.LocalNetworks = config.StringList{"192.168.1.0/24"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("the config the wizard builds is invalid: %v", err)
	}

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := writeConfig(path, &cfg); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCWOD_CONFIG", path)
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("the written config does not load: %v", err)
	}
	if loaded.Server.MAC != cfg.Server.MAC || loaded.Server.IP != cfg.Server.IP {
		t.Errorf("server section changed: %+v", loaded.Server)
	}
	if loaded.DuckDNS.Token != "secret-token" {
		t.Errorf("token = %q", loaded.DuckDNS.Token)
	}
	if !loaded.Transfer.Enabled || loaded.Transfer.Port != 25566 {
		t.Errorf("transfer section changed: %+v", loaded.Transfer)
	}
	if len(loaded.ParsedLocalNetworks()) != 1 {
		t.Error("local_networks did not survive the round trip")
	}
}

func TestWrittenConfigIsNotWorldReadable(t *testing.T) {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "192.168.1.100"
	cfg.Server.SSHUser = "someone"

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := writeConfig(path, &cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not map POSIX bits, the check is meaningful on Unix only.
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not POSIX bits on Windows")
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("mode is %04o, the file holds the DuckDNS token", info.Mode().Perm())
	}
}

func TestShellSafeStripsQuotes(t *testing.T) {
	if got := shellSafe("ssh-ed25519 AAAA'; rm -rf / #"); strings.Contains(got, "'") {
		t.Errorf("single quotes survived: %q", got)
	}
}
