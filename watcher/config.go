package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Watcher  WatcherConfig  `yaml:"watcher"`
	Server   ServerConfig   `yaml:"server"`
	WoL      WoLConfig      `yaml:"wol"`
	DuckDNS  DuckDNSConfig  `yaml:"duckdns"`
	Transfer TransferConfig `yaml:"transfer"`
	Timeouts TimeoutsConfig `yaml:"timeouts"`
	Limits   LimitsConfig   `yaml:"limits"`
	MOTD     MOTDConfig     `yaml:"motd"`

	// Path this was read from, assets are resolved next to it.
	Path string `yaml:"-"`
}

type WatcherConfig struct {
	ListenAddress string `yaml:"listen_address"`
	ListenPort    int    `yaml:"listen_port"`
}

type ServerConfig struct {
	MAC              string `yaml:"mac"`
	IP               string `yaml:"ip"`
	MCPort           int    `yaml:"mc_port"`
	SSHUser          string `yaml:"ssh_user"`
	SSHKeyPath       string `yaml:"ssh_key_path"`
	SSHStrictHostKey string `yaml:"ssh_strict_host_key"`
	SSHKnownHosts    string `yaml:"ssh_known_hosts"`
	ContainerName    string `yaml:"container_name"`
}

type WoLConfig struct {
	Mode             string `yaml:"mode"`
	BroadcastAddress string `yaml:"broadcast_address"`
}

type DuckDNSConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Domain              string `yaml:"domain"`
	Token               string `yaml:"token"`
	UpdateIntervalHours int    `yaml:"update_interval_hours"`
}

type TransferConfig struct {
	Enabled       bool       `yaml:"enabled"`
	Host          string     `yaml:"host"`
	Port          int        `yaml:"port"`
	LocalNetworks StringList `yaml:"local_networks"`
}

type TimeoutsConfig struct {
	BootTimeout    int `yaml:"boot_timeout"`
	MCReadyTimeout int `yaml:"mc_ready_timeout"`
}

type LimitsConfig struct {
	BootCooldown       int `yaml:"boot_cooldown"`
	BootFailureBackoff int `yaml:"boot_failure_backoff"`
	BootMaxBackoff     int `yaml:"boot_max_backoff"`
}

type MOTDConfig struct {
	Sleeping   string `yaml:"sleeping"`
	Starting   string `yaml:"starting"`
	LoginWait  string `yaml:"login_wait"`
	MaxPlayers int    `yaml:"max_players"`
}

// Accepts a YAML list or a single string with comma or space separated entries,
// because the matching env var can only ever be a string.
type StringList []string

func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	var list []string
	if err := node.Decode(&list); err == nil {
		*s = list
		return nil
	}
	var single string
	if err := node.Decode(&single); err != nil {
		return fmt.Errorf("expected a list or a string, got %s", node.Tag)
	}
	*s = splitList(single)
	return nil
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

const (
	// Two lines, which is what the server list shows.
	defaultMOTDSleeping = "{\"text\":\"Server currently asleep\",\"color\":\"gray\"," +
		"\"extra\":[{\"text\":\"\\nJoin to wake it up\",\"color\":\"green\"}]}"
	defaultMOTDStarting = "{\"text\":\"Server is starting\",\"color\":\"gold\"," +
		"\"extra\":[{\"text\":\"\\nGive it a moment, then join\",\"color\":\"gray\"}]}"
	defaultMOTDLoginWait = "{\"text\":\"Server is waking up. Please reconnect in a moment.\",\"color\":\"gold\"}"
)

var strictHostKeyModes = []string{"accept-new", "yes", "no"}

var containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func defaultConfig() Config {
	return Config{
		Watcher: WatcherConfig{ListenAddress: "0.0.0.0", ListenPort: 25565},
		Server: ServerConfig{
			MCPort:           25565,
			SSHStrictHostKey: "accept-new",
			ContainerName:    "minecraft",
		},
		WoL:      WoLConfig{Mode: "broadcast"},
		DuckDNS:  DuckDNSConfig{UpdateIntervalHours: 6},
		Transfer: TransferConfig{Port: 25566},
		Timeouts: TimeoutsConfig{BootTimeout: 60, MCReadyTimeout: 30},
		Limits:   LimitsConfig{BootCooldown: 10, BootFailureBackoff: 60, BootMaxBackoff: 900},
		MOTD: MOTDConfig{
			Sleeping:   defaultMOTDSleeping,
			Starting:   defaultMOTDStarting,
			LoginWait:  defaultMOTDLoginWait,
			MaxPlayers: 10,
		},
	}
}

// Env var, then repo root, then next to the binary.
func configSearchPaths() []string {
	paths := []string{}
	if env := os.Getenv("MC_WOL_CONFIG"); env != "" {
		paths = append(paths, env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(filepath.Dir(dir), "config.yml"),
			filepath.Join(dir, "config.yml"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(filepath.Dir(cwd), "config.yml"),
			filepath.Join(cwd, "config.yml"),
		)
	}
	return dedupe(paths)
}

func LoadConfig() (*Config, error) {
	searched := configSearchPaths()
	for _, p := range searched {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", p, err)
		}
		cfg := defaultConfig()
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("%s is not valid YAML: %w", p, err)
		}
		cfg.Path = p
		applyEnvOverrides(&cfg)
		log.Infof("Loading config from %s", p)
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		return &cfg, nil
	}
	return nil, fmt.Errorf(
		"no config.yml found, searched: %s\ncreate one with: cp config.example.yml config.yml",
		strings.Join(searched, ", "),
	)
}

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
				log.Warnf("Ignoring %s=%q, not a number", key, v)
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
	setString("WOL_MODE", &cfg.WoL.Mode)
	setString("WOL_BROADCAST_ADDRESS", &cfg.WoL.BroadcastAddress)
	setBool("DUCKDNS_ENABLED", &cfg.DuckDNS.Enabled)
	setString("DUCKDNS_DOMAIN", &cfg.DuckDNS.Domain)
	setString("DUCKDNS_TOKEN", &cfg.DuckDNS.Token)
	setInt("DUCKDNS_UPDATE_INTERVAL_HOURS", &cfg.DuckDNS.UpdateIntervalHours)
	setInt("BOOT_TIMEOUT", &cfg.Timeouts.BootTimeout)
	setInt("MC_READY_TIMEOUT", &cfg.Timeouts.MCReadyTimeout)
	setInt("BOOT_COOLDOWN", &cfg.Limits.BootCooldown)
	setInt("BOOT_FAILURE_BACKOFF", &cfg.Limits.BootFailureBackoff)
	setInt("BOOT_MAX_BACKOFF", &cfg.Limits.BootMaxBackoff)
	setBool("TRANSFER_ENABLED", &cfg.Transfer.Enabled)
	setString("TRANSFER_HOST", &cfg.Transfer.Host)
	setInt("TRANSFER_PORT", &cfg.Transfer.Port)
	if v, ok := os.LookupEnv("TRANSFER_LOCAL_NETWORKS"); ok {
		cfg.Transfer.LocalNetworks = splitList(v)
	}
}

// These messages are read by people setting this up for the first time, so they
// say what to put in, not only what is wrong.
func (c *Config) Validate() error {
	if c.Server.MAC == "" {
		return fmt.Errorf("server.mac is missing, put the MAC address of your server PC there, for example AA:BB:CC:DD:EE:FF")
	}
	if _, err := ParseMAC(c.Server.MAC); err != nil {
		return fmt.Errorf("server.mac %q is not a MAC address, it needs 6 hex pairs like AA:BB:CC:DD:EE:FF", c.Server.MAC)
	}
	if c.Server.IP == "" {
		return fmt.Errorf("server.ip is missing, put the local IP of your server PC there, for example 192.168.1.100")
	}
	if net.ParseIP(c.Server.IP) == nil {
		return fmt.Errorf("server.ip %q is not an IP address", c.Server.IP)
	}
	if c.Server.SSHUser == "" {
		return fmt.Errorf("server.ssh_user is missing, put your login name on the server PC there")
	}
	if c.Server.ContainerName == "" {
		return fmt.Errorf("server.container_name is empty, it has to name the Minecraft container, usually 'minecraft'")
	}
	// The name is pasted into a remote shell command, so it stays inside what
	// Docker accepts as a container name.
	if !containerNamePattern.MatchString(c.Server.ContainerName) {
		return fmt.Errorf("server.container_name %q is not a Docker container name, "+
			"use letters, digits, underscore, dot or dash", c.Server.ContainerName)
	}
	if c.Watcher.ListenPort < 1 || c.Watcher.ListenPort > 65535 {
		return fmt.Errorf("watcher.listen_port is %d, it has to be between 1 and 65535", c.Watcher.ListenPort)
	}
	if c.Server.MCPort < 1 || c.Server.MCPort > 65535 {
		return fmt.Errorf("server.mc_port is %d, it has to be between 1 and 65535", c.Server.MCPort)
	}
	if c.Watcher.ListenAddress == "" {
		c.Watcher.ListenAddress = "0.0.0.0"
	}

	c.WoL.Mode = strings.ToLower(strings.TrimSpace(c.WoL.Mode))
	switch c.WoL.Mode {
	case "broadcast":
		if c.WoL.BroadcastAddress == "" {
			c.WoL.BroadcastAddress = "255.255.255.255"
			log.Warnf("wol.broadcast_address is empty, falling back to 255.255.255.255")
		}
		if net.ParseIP(c.WoL.BroadcastAddress) == nil {
			return fmt.Errorf("wol.broadcast_address %q is not an IP address", c.WoL.BroadcastAddress)
		}
	case "unicast":
	default:
		return fmt.Errorf("wol.mode is %q, it has to be 'broadcast' or 'unicast'", c.WoL.Mode)
	}

	c.Server.SSHStrictHostKey = strings.ToLower(strings.TrimSpace(c.Server.SSHStrictHostKey))
	if !contains(strictHostKeyModes, c.Server.SSHStrictHostKey) {
		log.Warnf("Invalid server.ssh_strict_host_key %q, falling back to 'accept-new'", c.Server.SSHStrictHostKey)
		c.Server.SSHStrictHostKey = "accept-new"
	}
	if c.Server.SSHStrictHostKey == "no" {
		log.Warnf("server.ssh_strict_host_key is 'no', any host key is accepted, " +
			"which allows man-in-the-middle attacks on the SSH connection")
	}

	if c.DuckDNS.Enabled {
		if c.DuckDNS.Domain == "" || c.DuckDNS.Token == "" {
			return fmt.Errorf("duckdns.enabled is true but domain or token is missing, set duckdns.enabled: false if you do not use it")
		}
		if strings.Contains(c.DuckDNS.Domain, ".") {
			return fmt.Errorf("duckdns.domain %q contains a dot, use only the subdomain without .duckdns.org", c.DuckDNS.Domain)
		}
		if c.DuckDNS.UpdateIntervalHours < 1 {
			return fmt.Errorf("duckdns.update_interval_hours is %d, it has to be at least 1", c.DuckDNS.UpdateIntervalHours)
		}
	}

	if c.Transfer.Enabled {
		if c.Transfer.Host == "" {
			return fmt.Errorf("transfer.enabled is true but transfer.host is missing, put the public hostname players get sent to there")
		}
		if c.Transfer.Port < 1 || c.Transfer.Port > 65535 {
			return fmt.Errorf("transfer.port is %d, it has to be between 1 and 65535", c.Transfer.Port)
		}
	}

	if c.Timeouts.BootTimeout < 1 {
		return fmt.Errorf("timeouts.boot_timeout is %d, it has to be at least 1", c.Timeouts.BootTimeout)
	}
	if c.Timeouts.MCReadyTimeout < 1 {
		return fmt.Errorf("timeouts.mc_ready_timeout is %d, it has to be at least 1", c.Timeouts.MCReadyTimeout)
	}

	if c.MOTD.MaxPlayers < 0 {
		return fmt.Errorf("motd.max_players is %d, it cannot be negative", c.MOTD.MaxPlayers)
	}
	for _, m := range []struct {
		name  string
		value string
	}{
		{"motd.sleeping", c.MOTD.Sleeping},
		{"motd.starting", c.MOTD.Starting},
		{"motd.login_wait", c.MOTD.LoginWait},
	} {
		if !json.Valid([]byte(m.value)) {
			return fmt.Errorf("%s is not valid JSON, it has to look like %s", m.name, defaultMOTDSleeping)
		}
	}
	return nil
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
			log.Warnf("Ignoring invalid network in transfer.local_networks: %s", entry)
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

// Empty means the platform default, matching what the ssh client would pick.
func (c *Config) ResolvedSSHKeyPath() string {
	if c.Server.SSHKeyPath != "" {
		return expandHome(c.Server.SSHKeyPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "id_ed25519"
	}
	return filepath.Join(home, ".ssh", "id_ed25519")
}

func (c *Config) ResolvedKnownHostsPath() string {
	if c.Server.SSHKnownHosts != "" {
		return expandHome(c.Server.SSHKnownHosts)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "known_hosts"
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimLeft(p[1:], "/\\"))
		}
	}
	return p
}

func ParseMAC(mac string) ([]byte, error) {
	cleaned := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(mac)
	if len(cleaned) != 12 {
		return nil, fmt.Errorf("expected 12 hex digits, got %d", len(cleaned))
	}
	return hex.DecodeString(cleaned)
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func dedupe(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
