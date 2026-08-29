package config

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/fsx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func NormalizeDuckDNSDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimSuffix(domain, ".")
	domain = strings.TrimSuffix(domain, ".duckdns.org")
	return strings.TrimSuffix(domain, ".")
}

// The full name players connect to.
func (c *Config) DuckDNSHost() string {
	if c.DuckDNS.Domain == "" {
		return ""
	}
	return c.DuckDNS.Domain + ".duckdns.org"
}

// Invalid entries are dropped with a warning so one typo does not stop the watcher.
func (c *Config) ParsedLocalNetworks() []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(c.Transfer.LocalNetworks))
	for _, entry := range c.Transfer.LocalNetworks {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		_, n, err := net.ParseCIDR(entry)
		if err != nil {
			logging.Warnf("Ignoring invalid network in transfer.local_networks: %s", entry)
			continue
		}
		nets = append(nets, n)
	}
	return nets
}

// Assets live next to the config, with the binary directory as fallback. The
// Python version only looked next to the config, which missed them on Windows.
func (c *Config) AssetsDir() string {
	candidates := []string{}
	if c.Path != "" {
		if abs, err := filepath.Abs(c.Path); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(abs), "assets"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "assets"))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "assets"
}

// Version and player slots from the last status probe, cached so the watcher can
// answer for the server while it sleeps.
// Each world runs its own Minecraft version, so what was learned for one says
// nothing about the next. Installations without worlds share the one key.
func (c *Config) ServerInfoKey() string {
	if world := c.ActiveWorldName(); world != "" {
		return world
	}
	return "default"
}

func (c *Config) ServerInfoPath() string {
	const name = ".server-info.json"
	if c.Path != "" {
		if abs, err := filepath.Abs(c.Path); err == nil {
			return filepath.Join(filepath.Dir(abs), name)
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), name)
	}
	return name
}

// Empty means the platform default, matching what the ssh client would pick.
// Its own key, so the watcher never adopts the personal one the user logs in
// with everywhere else. An install from before this stays on the old path.
func (c *Config) ResolvedSSHKeyPath() string {
	if c.Server.SSHKeyPath != "" {
		return fsx.ExpandHome(c.Server.SSHKeyPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return WatcherKeyName
	}
	return filepath.Join(home, ".ssh", WatcherKeyName)
}

// Only reachable by writing the path into the config by hand. Nothing picks a
// shared key on its own, and check says so when someone did.
func (c *Config) UsesSharedSSHKey() bool {
	return filepath.Base(c.ResolvedSSHKeyPath()) == SharedKeyName
}

func (c *Config) ResolvedKnownHostsPath() string {
	if c.Server.SSHKnownHosts != "" {
		return fsx.ExpandHome(c.Server.SSHKnownHosts)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "known_hosts"
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func ParseMAC(mac string) ([]byte, error) {
	cleaned := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(mac)
	if len(cleaned) != 12 {
		return nil, fmt.Errorf("expected 12 hex digits, got %d", len(cleaned))
	}
	return hex.DecodeString(cleaned)
}
