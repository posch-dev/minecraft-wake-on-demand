package netprobe

import (
	"errors"
	"testing"
)

func TestGuessBroadcast(t *testing.T) {
	cases := map[string]string{
		"192.168.1.100": "192.168.1.255",
		"10.0.0.5":      "10.0.0.255",
		"172.16.4.9":    "172.16.4.255",
		"not-an-ip":     "255.255.255.255",
	}
	for in, want := range cases {
		if got := GuessBroadcast(in); got != want {
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

var errNotAnIP = errors.New("not an IP address")
