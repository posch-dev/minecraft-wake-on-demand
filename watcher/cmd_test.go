package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

func TestGuessBroadcast(t *testing.T) {
	cases := map[string]string{
		"192.168.1.100": "192.168.1.255",
		"10.0.0.5":      "10.0.0.255",
		"172.16.4.9":    "172.16.4.255",
		"not-an-ip":     "255.255.255.255",
	}
	for in, want := range cases {
		if got := guessBroadcast(in); got != want {
			t.Errorf("guessBroadcast(%q) = %q, want %q", in, got, want)
		}
	}
}

// The arp output is localised on Windows and formatted differently per
// platform, so the address is found by pattern instead of by column.
func TestMACExtractionFromARPOutput(t *testing.T) {
	samples := map[string]struct {
		line string
		want string
	}{
		"windows german":  {"  192.168.1.1         aa-bb-cc-dd-ee-ff     dynamisch", "AA:BB:CC:DD:EE:FF"},
		"windows english": {"  192.168.1.1         aa-bb-cc-dd-ee-ff     dynamic", "AA:BB:CC:DD:EE:FF"},
		"linux":           {"router (192.168.1.1) at aa:bb:cc:dd:ee:ff [ether] on eth0", "AA:BB:CC:DD:EE:FF"},
		"macos":           {"? (192.168.1.1) at aa:bb:cc:dd:ee:ff on en0 ifscope [ethernet]", "AA:BB:CC:DD:EE:FF"},
	}
	for name, sample := range samples {
		t.Run(name, func(t *testing.T) {
			if !lineMentionsIP(sample.line, "192.168.1.1") {
				t.Fatal("the IP was not recognised in the line")
			}
			mac := macPattern.FindString(sample.line)
			if mac == "" {
				t.Fatal("no MAC found")
			}
			if got := normalizeMAC(mac); got != sample.want {
				t.Errorf("got %q, want %q", got, sample.want)
			}
		})
	}
}

func TestLineMentionsIPDoesNotMatchPrefixes(t *testing.T) {
	if lineMentionsIP("  192.168.1.10   aa-bb-cc-dd-ee-ff  dynamic", "192.168.1.1") {
		t.Error("192.168.1.10 must not match a lookup for 192.168.1.1")
	}
}

func TestNullMACIsRejected(t *testing.T) {
	for _, mac := range []string{"00:00:00:00:00:00", "ff-ff-ff-ff-ff-ff"} {
		if !isNullMAC(mac) {
			t.Errorf("%s should count as a null MAC", mac)
		}
	}
	if isNullMAC("AA:BB:CC:DD:EE:FF") {
		t.Error("a real MAC should not count as null")
	}
}

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

func TestPrompterUsesFallbackOnEmptyInput(t *testing.T) {
	p := ui.NewPrompterFrom(strings.NewReader("\n\n\n"))
	if got := p.Line("q", "default"); got != "default" {
		t.Errorf("got %q, want the fallback", got)
	}
	if !p.YesNo("q", true) {
		t.Error("empty answer should take the true fallback")
	}
	if p.YesNo("q", false) {
		t.Error("empty answer should take the false fallback")
	}
}

func TestPrompterYesNoAcceptsGermanAndEnglish(t *testing.T) {
	p := ui.NewPrompterFrom(strings.NewReader("yes\nja\nn\nnein\n"))
	for i, want := range []bool{true, true, false, false} {
		if got := p.YesNo("q", !want); got != want {
			t.Errorf("answer %d = %v, want %v", i, got, want)
		}
	}
}

// The wizard must not give up on a typo, it asks again.
func TestPrompterRetriesUntilValid(t *testing.T) {
	p := ui.NewPrompterFrom(strings.NewReader("nonsense\n999.999.1.1\n192.168.1.50\n"))
	got := p.Validated("ip", "", func(v string) error {
		if guessBroadcast(v) == "255.255.255.255" {
			return errNotAnIP
		}
		return nil
	})
	if got != "192.168.1.50" {
		t.Errorf("got %q, want the third answer", got)
	}
}

func TestShellSafeStripsQuotes(t *testing.T) {
	if got := shellSafe("ssh-ed25519 AAAA'; rm -rf / #"); strings.Contains(got, "'") {
		t.Errorf("single quotes survived: %q", got)
	}
}

var errNotAnIP = errors.New("not an IP address")
