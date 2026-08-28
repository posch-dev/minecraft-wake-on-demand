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

// The example is what people copy, so it has to agree with the defaults.
// Otherwise it documents something the watcher does not do.
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

// The server list shows two lines, so an example that only fills one teaches
// the wrong shape.
func TestExampleMOTDsRenderTwoLines(t *testing.T) {
	for _, file := range []string{
		"assets/examples/motd-sleeping.json",
		"assets/examples/motd-starting.json",
		"assets/examples/motd-live.json",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		lines := motdLines(t, strings.TrimSpace(string(data)))
		if len(lines) != 2 {
			t.Errorf("%s renders %d line(s): %q", file, len(lines), lines)
		}
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				t.Errorf("%s line %d is empty", file, i+1)
			}
		}
		// Obviously a placeholder, so nobody keeps it by accident.
		if !strings.Contains(strings.ToUpper(string(data)), "CHANGE THIS") {
			t.Errorf("%s does not read as something to replace", file)
		}
	}
}

// A colour Minecraft does not know makes the whole entry fall back to white.
func TestExampleMOTDsUseKnownColours(t *testing.T) {
	known := map[string]bool{}
	for _, name := range []string{
		"black", "dark_blue", "dark_green", "dark_aqua", "dark_red", "dark_purple",
		"gold", "gray", "dark_gray", "blue", "green", "aqua", "red",
		"light_purple", "yellow", "white",
	} {
		known[name] = true
	}

	for _, file := range []string{
		"assets/examples/motd-sleeping.json",
		"assets/examples/motd-starting.json",
		"assets/examples/motd-live.json",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var component struct {
			Color string `json:"color"`
			Extra []struct {
				Color string `json:"color"`
			} `json:"extra"`
		}
		if err := json.Unmarshal(data, &component); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		colours := []string{component.Color}
		for _, e := range component.Extra {
			colours = append(colours, e.Color)
		}
		for _, colour := range colours {
			if colour != "" && !known[colour] {
				t.Errorf("%s uses %q, which Minecraft does not know", file, colour)
			}
		}
	}
}

// The picture has to be exactly what the watcher accepts, or the example is a
// trap rather than a starting point.
func TestExampleIconIsAValid64x64PNG(t *testing.T) {
	data, err := os.ReadFile("assets/examples/server-icon.png")
	if err != nil {
		t.Fatal(err)
	}

	width, height, err := pngDimensions(data)
	if err != nil {
		t.Fatalf("the example icon is not a PNG: %v", err)
	}
	if width != iconEdge || height != iconEdge {
		t.Errorf("the example icon is %dx%d, want %dx%d", width, height, iconEdge, iconEdge)
	}
	if len(data) > maxIconBytes {
		t.Errorf("the example icon is %d bytes, over the %d byte limit", len(data), maxIconBytes)
	}
}
