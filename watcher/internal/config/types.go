package config

import (
	"fmt"
	"regexp"
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
	Sleep    SleepConfig    `yaml:"sleep"`
	Worlds   WorldsConfig   `yaml:"worlds"`
	// Asked once, so a second world does not ask for the licence again.
	EULAAccepted bool         `yaml:"eula_accepted"`
	Update       UpdateConfig `yaml:"update"`
	MOTD         MOTDConfig   `yaml:"motd"`

	// Path this was read from, assets are resolved next to it.
	Path string `yaml:"-"`
}

type WatcherConfig struct {
	ListenAddress    string     `yaml:"listen_address"`
	ListenPort       int        `yaml:"listen_port"`
	AllowedHostnames StringList `yaml:"allowed_hostnames"`
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
	// Where the compose file lives on the server, remembered so nothing has to
	// ask for it again.
	ComposeDir string `yaml:"compose_dir"`
	// True once setup-ssh installed the helper script. The watcher then sends
	// verbs instead of whole commands, see remotehelper.go.
	RemoteHelper bool `yaml:"remote_helper"`
}

// Only ever asks GitHub whether something newer exists, which also tells GitHub
// this machine's IP. Nothing is ever installed without being asked.
type UpdateConfig struct {
	Check bool `yaml:"check"`
}

// Putting the server PC back to sleep once nobody is playing. Off by default,
// it needs the helper script and a sudoers line that setup-ssh installs.
type SleepConfig struct {
	Enabled      bool   `yaml:"enabled"`
	IdleAfter    int    `yaml:"idle_after"`
	ConfirmDelay int    `yaml:"confirm_delay"`
	GracePeriod  int    `yaml:"grace_period"`
	PollInterval int    `yaml:"poll_interval"`
	Action       string `yaml:"action"`
	Command      string `yaml:"command"`
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
	MaxLogins          int `yaml:"max_logins"`
	MaxPerIP           int `yaml:"max_per_ip"`
}

type MOTDConfig struct {
	Sleeping string `yaml:"sleeping"`
	Starting string `yaml:"starting"`
	// Empty leaves the running server's own MOTD alone.
	Live       string `yaml:"live"`
	LoginWait  string `yaml:"login_wait"`
	ServerFull string `yaml:"server_full"`
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
	*s = SplitList(single)
	return nil
}

func SplitList(value string) []string {
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
	DefaultMOTDSleeping = "{\"text\":\"Server currently asleep\",\"color\":\"gray\"," +
		"\"extra\":[{\"text\":\"\\nJoin to wake it up\",\"color\":\"green\"}]}"
	DefaultMOTDStarting = "{\"text\":\"Server is starting\",\"color\":\"gold\"," +
		"\"extra\":[{\"text\":\"\\nGive it a moment, then join\",\"color\":\"gray\"}]}"
	DefaultMOTDLoginWait  = "{\"text\":\"Server is waking up. Please reconnect in a moment.\",\"color\":\"gold\"}"
	DefaultMOTDServerFull = "{\"text\":\"Server is full. Please try again in a moment.\",\"color\":\"red\"}"
)

const (
	WatcherKeyName = "mcwod"
	SharedKeyName  = "id_ed25519"
)

var strictHostKeyModes = []string{"accept-new", "yes", "no"}

var SleepActions = []string{"suspend", "hibernate", "shutdown", "custom"}

var ContainerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
