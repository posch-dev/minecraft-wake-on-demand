package netprobe

import (
	"net"
	"testing"
)

func netsFrom(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets := []*net.IPNet{}
	for _, cidr := range cidrs {
		ip, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip.Mask(network.Mask)
		nets = append(nets, network)
	}
	return nets
}

// Assuming /24 is wrong on anything wider, and the only symptom is that waking
// never works.
func TestBroadcastFollowsTheRealMask(t *testing.T) {
	cases := []struct {
		cidr   string
		server string
		want   string
	}{
		{"192.168.1.50/24", "192.168.1.100", "192.168.1.255"},
		{"192.168.1.50/16", "192.168.99.100", "192.168.255.255"},
		{"10.0.4.50/22", "10.0.5.100", "10.0.7.255"},
		{"172.16.0.50/12", "172.20.1.1", "172.31.255.255"},
		{"192.168.1.50/25", "192.168.1.100", "192.168.1.127"},
	}
	for _, c := range cases {
		got := BroadcastForIP(net.ParseIP(c.server).To4(), netsFrom(t, c.cidr))
		if got != c.want {
			t.Errorf("%s with the watcher on %s = %q, want %q", c.server, c.cidr, got, c.want)
		}
	}
}

func TestBroadcastPicksTheMatchingInterface(t *testing.T) {
	nets := netsFrom(t, "10.8.0.2/24", "192.168.1.50/24", "172.17.0.1/16")

	if got := BroadcastForIP(net.ParseIP("192.168.1.100").To4(), nets); got != "192.168.1.255" {
		t.Errorf("got %q, want the LAN interface to win", got)
	}
	if got := BroadcastForIP(net.ParseIP("172.17.0.9").To4(), nets); got != "172.17.255.255" {
		t.Errorf("got %q, want the docker interface", got)
	}
}

// Server in another network, nothing to compute from, so the caller falls back.
func TestBroadcastReportsWhenNothingMatches(t *testing.T) {
	nets := netsFrom(t, "192.168.1.50/24")

	if got := BroadcastForIP(net.ParseIP("10.9.9.9").To4(), nets); got != "" {
		t.Errorf("got %q, want an empty answer so the caller can ask", got)
	}
}

func TestGuessBroadcastStillAnswersForRubbish(t *testing.T) {
	if got := GuessBroadcast("not-an-ip"); got != "255.255.255.255" {
		t.Errorf("got %q, want the all subnets fallback", got)
	}
}
