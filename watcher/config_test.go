package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Sections left out here are appended by individual tests, so this must not
// already contain them.
const minimalConfig = `
server:
  mac: "AA:BB:CC:DD:EE:FF"
  ip: "192.168.1.100"
  ssh_user: "someone"
`

func loadFrom(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCWOD_CONFIG", path)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func TestDefaultsFillIn(t *testing.T) {
	cfg := loadFrom(t, minimalConfig)
	if cfg.Watcher.ListenPort != 25565 {
		t.Errorf("listen_port = %d, want 25565", cfg.Watcher.ListenPort)
	}
	if cfg.Server.ContainerName != "minecraft" {
		t.Errorf("container_name = %q, want minecraft", cfg.Server.ContainerName)
	}
	if cfg.Server.SSHStrictHostKey != "accept-new" {
		t.Errorf("ssh_strict_host_key = %q, want accept-new", cfg.Server.SSHStrictHostKey)
	}
	if cfg.Limits.BootMaxBackoff != 900 {
		t.Errorf("boot_max_backoff = %d, want 900", cfg.Limits.BootMaxBackoff)
	}
	if cfg.MOTD.MaxPlayers != 10 {
		t.Errorf("max_players = %d, want 10", cfg.MOTD.MaxPlayers)
	}
}

func TestEnvOverridesWin(t *testing.T) {
	t.Setenv("WATCHER_LISTEN_PORT", "25599")
	t.Setenv("SERVER_CONTAINER_NAME", "mc-other")
	t.Setenv("TRANSFER_ENABLED", "TRUE")
	t.Setenv("TRANSFER_HOST", "example.duckdns.org")
	t.Setenv("TRANSFER_LOCAL_NETWORKS", "10.0.0.0/8, 192.168.0.0/16")

	cfg := loadFrom(t, minimalConfig)
	if cfg.Watcher.ListenPort != 25599 {
		t.Errorf("listen_port = %d, want 25599", cfg.Watcher.ListenPort)
	}
	if cfg.Server.ContainerName != "mc-other" {
		t.Errorf("container_name = %q, want mc-other", cfg.Server.ContainerName)
	}
	if !cfg.Transfer.Enabled {
		t.Error("transfer.enabled should be true")
	}
	if len(cfg.Transfer.LocalNetworks) != 2 {
		t.Errorf("local_networks = %v, want 2 entries", cfg.Transfer.LocalNetworks)
	}
}

func TestLocalNetworksAcceptsListAndString(t *testing.T) {
	var asList struct {
		N StringList `yaml:"n"`
	}
	if err := yaml.Unmarshal([]byte("n: [\"10.0.0.0/8\", \"192.168.1.0/24\"]"), &asList); err != nil {
		t.Fatal(err)
	}
	if len(asList.N) != 2 {
		t.Errorf("list form gave %v", asList.N)
	}

	var asString struct {
		N StringList `yaml:"n"`
	}
	if err := yaml.Unmarshal([]byte("n: \"10.0.0.0/8, 192.168.1.0/24\""), &asString); err != nil {
		t.Fatal(err)
	}
	if len(asString.N) != 2 {
		t.Errorf("string form gave %v", asString.N)
	}
}

func TestInvalidNetworksAreDropped(t *testing.T) {
	cfg := loadFrom(t, minimalConfig+`
transfer:
  enabled: true
  host: "example.duckdns.org"
  local_networks: ["192.168.1.0/24", "not-a-network"]
`)
	nets := cfg.ParsedLocalNetworks()
	if len(nets) != 1 {
		t.Errorf("got %d networks, want 1", len(nets))
	}
}

func TestValidationRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"missing mac":      "server:\n  ip: \"192.168.1.1\"\n  ssh_user: \"x\"\n",
		"bad mac":          "server:\n  mac: \"nope\"\n  ip: \"192.168.1.1\"\n  ssh_user: \"x\"\n",
		"bad ip":           "server:\n  mac: \"AA:BB:CC:DD:EE:FF\"\n  ip: \"999.1.1.1\"\n  ssh_user: \"x\"\n",
		"missing ssh_user": "server:\n  mac: \"AA:BB:CC:DD:EE:FF\"\n  ip: \"192.168.1.1\"\n",
		"bad wol mode":     minimalConfig + "\nwol:\n  mode: \"magic\"\n",
		"duckdns no token": minimalConfig + "\nduckdns:\n  enabled: true\n  domain: \"mine\"\n",
		"bad motd json":    minimalConfig + "\nmotd:\n  sleeping: \"{not json\"\n",
		"transfer no host": minimalConfig + "\ntransfer:\n  enabled: true\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MCWOD_CONFIG", path)
			if _, err := LoadConfig(); err == nil {
				t.Error("expected an error, got none")
			}
		})
	}
}

func TestParseMACForms(t *testing.T) {
	for _, form := range []string{"AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff", "aabbccddeeff"} {
		got, err := ParseMAC(form)
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		want := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s decoded to %x", form, got)
			}
		}
	}
	if _, err := ParseMAC("AA:BB:CC:DD:EE"); err == nil {
		t.Error("short MAC should fail")
	}
	if _, err := ParseMAC("ZZ:BB:CC:DD:EE:FF"); err == nil {
		t.Error("non hex MAC should fail")
	}
}

func TestStrictHostKeyFallsBack(t *testing.T) {
	cfg := loadFrom(t, "server:\n  mac: \"AA:BB:CC:DD:EE:FF\"\n  ip: \"192.168.1.100\"\n"+
		"  ssh_user: \"someone\"\n  ssh_strict_host_key: \"maybe\"\n")
	if cfg.Server.SSHStrictHostKey != "accept-new" {
		t.Errorf("got %q, want accept-new", cfg.Server.SSHStrictHostKey)
	}
}

func TestBroadcastAddressFallsBack(t *testing.T) {
	cfg := loadFrom(t, minimalConfig)
	if cfg.WoL.BroadcastAddress != "255.255.255.255" {
		t.Errorf("got %q, want 255.255.255.255", cfg.WoL.BroadcastAddress)
	}
}

func motdLines(t *testing.T, raw string) []string {
	t.Helper()
	var component struct {
		Text  string `json:"text"`
		Extra []struct {
			Text string `json:"text"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(raw), &component); err != nil {
		t.Fatalf("not valid MOTD JSON: %v\n%s", err, raw)
	}
	text := component.Text
	for _, e := range component.Extra {
		text += e.Text
	}
	return strings.Split(text, "\n")
}

// config.example.yml is the file people copy, so it has to agree with the
// built in defaults. Otherwise the example quietly documents something the
// watcher does not do.
func TestExampleConfigMatchesTheDefaults(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var example Config
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatalf("config.example.yml is not valid YAML: %v", err)
	}

	defaults := defaultConfig()
	for _, c := range []struct {
		name    string
		example string
		builtin string
	}{
		{"motd.sleeping", example.MOTD.Sleeping, defaults.MOTD.Sleeping},
		{"motd.starting", example.MOTD.Starting, defaults.MOTD.Starting},
		{"motd.login_wait", example.MOTD.LoginWait, defaults.MOTD.LoginWait},
	} {
		var a, b any
		if err := json.Unmarshal([]byte(c.example), &a); err != nil {
			t.Errorf("%s in config.example.yml is not valid JSON: %v", c.name, err)
			continue
		}
		if err := json.Unmarshal([]byte(c.builtin), &b); err != nil {
			t.Errorf("%s default is not valid JSON: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s differs\n  example: %s\n  default: %s", c.name, c.example, c.builtin)
		}
	}

	if example.MOTD.MaxPlayers != defaults.MOTD.MaxPlayers {
		t.Errorf("motd.max_players: example %d, default %d",
			example.MOTD.MaxPlayers, defaults.MOTD.MaxPlayers)
	}
}

// The server list shows two lines, so both MOTDs use both of them.
func TestExampleMOTDsRenderTwoLines(t *testing.T) {
	for _, c := range []struct {
		file string
		// Empty for live, which has no built in default by design.
		builtin string
	}{
		{"assets/examples/motd-sleeping.json", defaultMOTDSleeping},
		{"assets/examples/motd-starting.json", defaultMOTDStarting},
		{"assets/examples/motd-live.json", ""},
	} {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		lines := motdLines(t, strings.TrimSpace(string(data)))
		if len(lines) != 2 {
			t.Errorf("%s renders %d line(s): %q", c.file, len(lines), lines)
		}
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				t.Errorf("%s line %d is empty", c.file, i+1)
			}
		}

		if c.builtin == "" {
			continue
		}
		builtinLines := motdLines(t, c.builtin)
		if !reflect.DeepEqual(lines, builtinLines) {
			t.Errorf("%s and the built in default show different text\n  file:    %q\n  builtin: %q",
				c.file, lines, builtinLines)
		}
	}
}
