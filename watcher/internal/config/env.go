package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func applyEnvOverrides(cfg *Config) {
	setString := func(key string, target *string) {
		if v, ok := os.LookupEnv(key); ok {
			*target = v
		}
	}
	setInt := func(key string, target *int) {
		if v, ok := os.LookupEnv(key); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*target = n
			} else {
				logging.Warnf("Ignoring %s=%q, not a number", key, v)
			}
		}
	}
	setBool := func(key string, target *bool) {
		if v, ok := os.LookupEnv(key); ok {
			*target = strings.EqualFold(v, "true")
		}
	}

	setString("SERVER_MAC", &cfg.Server.MAC)
	setString("SERVER_IP", &cfg.Server.IP)
	setInt("SERVER_MC_PORT", &cfg.Server.MCPort)
	setString("SERVER_SSH_USER", &cfg.Server.SSHUser)
	setString("SERVER_SSH_KEY_PATH", &cfg.Server.SSHKeyPath)
	setString("SERVER_SSH_STRICT_HOST_KEY", &cfg.Server.SSHStrictHostKey)
	setString("SERVER_SSH_KNOWN_HOSTS", &cfg.Server.SSHKnownHosts)
	setString("SERVER_CONTAINER_NAME", &cfg.Server.ContainerName)
	setString("WATCHER_LISTEN_ADDRESS", &cfg.Watcher.ListenAddress)
	setInt("WATCHER_LISTEN_PORT", &cfg.Watcher.ListenPort)
	if v, ok := os.LookupEnv("WATCHER_ALLOWED_HOSTNAMES"); ok {
		cfg.Watcher.AllowedHostnames = SplitList(v)
	}
	setString("WOL_MODE", &cfg.WoL.Mode)
	setString("WOL_BROADCAST_ADDRESS", &cfg.WoL.BroadcastAddress)
	setBool("DUCKDNS_ENABLED", &cfg.DuckDNS.Enabled)
	setBool("UPDATE_CHECK", &cfg.Update.Check)
	setString("DUCKDNS_DOMAIN", &cfg.DuckDNS.Domain)
	setString("DUCKDNS_TOKEN", &cfg.DuckDNS.Token)
	setInt("DUCKDNS_UPDATE_INTERVAL_HOURS", &cfg.DuckDNS.UpdateIntervalHours)
	setInt("BOOT_TIMEOUT", &cfg.Timeouts.BootTimeout)
	setInt("MC_READY_TIMEOUT", &cfg.Timeouts.MCReadyTimeout)
	setInt("BOOT_COOLDOWN", &cfg.Limits.BootCooldown)
	setInt("BOOT_FAILURE_BACKOFF", &cfg.Limits.BootFailureBackoff)
	setInt("BOOT_MAX_BACKOFF", &cfg.Limits.BootMaxBackoff)
	setInt("MAX_LOGINS", &cfg.Limits.MaxLogins)
	setInt("MAX_PER_IP", &cfg.Limits.MaxPerIP)
	setBool("TRANSFER_ENABLED", &cfg.Transfer.Enabled)
	setString("TRANSFER_HOST", &cfg.Transfer.Host)
	setInt("TRANSFER_PORT", &cfg.Transfer.Port)
	if v, ok := os.LookupEnv("TRANSFER_LOCAL_NETWORKS"); ok {
		cfg.Transfer.LocalNetworks = SplitList(v)
	}
}

// These messages are read by people setting this up for the first time, so they
// say what to put in, not only what is wrong.

func RenamedEnv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	old := strings.Replace(key, "MCWOD_", "MC_WOL_", 1)
	value := os.Getenv(old)
	if value != "" {
		logging.Warnf("%s is the old name for %s, it still works but rename it", old, key)
	}
	return value
}

// Both spellings people actually type, so the suffix is neither demanded nor
// refused. A subdomain of its own is left alone, only ours is trimmed.
