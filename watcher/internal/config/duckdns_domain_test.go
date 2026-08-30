package config

import (
	"testing"
)

// Whichever way somebody types it, both are what people actually have in front
// of them when they copy it out of DuckDNS.
func TestDuckDNSDomainAcceptsBothSpellings(t *testing.T) {
	cases := map[string]string{
		"eliahmc":                 "eliahmc",
		"eliahmc.duckdns.org":     "eliahmc",
		"ELIAHMC.DuckDNS.org":     "eliahmc",
		"  eliahmc.duckdns.org  ": "eliahmc",
		"eliahmc.duckdns.org.":    "eliahmc",
		"":                        "",
	}
	for in, want := range cases {
		if got := NormalizeDuckDNSDomain(in); got != want {
			t.Errorf("NormalizeDuckDNSDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// Somebody's own subdomain under duckdns is still theirs, only our suffix goes.
func TestDuckDNSDomainKeepsAForeignSubdomain(t *testing.T) {
	if got := NormalizeDuckDNSDomain("mc.eliahmc.duckdns.org"); got != "mc.eliahmc" {
		t.Errorf("got %q, want mc.eliahmc", got)
	}
}

func TestDuckDNSHostAddsTheSuffixBack(t *testing.T) {
	cfg := Default()
	cfg.DuckDNS.Domain = "eliahmc"

	if got := cfg.DuckDNSHost(); got != "eliahmc.duckdns.org" {
		t.Errorf("host = %q", got)
	}
	cfg.DuckDNS.Domain = ""
	if got := cfg.DuckDNSHost(); got != "" {
		t.Errorf("host = %q, want empty without a domain", got)
	}
}

// It used to be a hard error, which is a strange thing to refuse when it is
// the form printed on the DuckDNS page.
func TestFullAddressNoLongerFailsValidation(t *testing.T) {
	cfg := Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "192.168.1.100"
	cfg.Server.SSHUser = "eliah"
	cfg.WoL.BroadcastAddress = "192.168.1.255"
	cfg.DuckDNS.Enabled = true
	cfg.DuckDNS.Domain = "eliahmc.duckdns.org"
	cfg.DuckDNS.Token = "secret"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("the full address should validate: %v", err)
	}
	if cfg.DuckDNS.Domain != "eliahmc" {
		t.Errorf("domain = %q, it should have been trimmed on load", cfg.DuckDNS.Domain)
	}
	if len(cfg.Watcher.AllowedHostnames) != 1 || cfg.Watcher.AllowedHostnames[0] != "eliahmc.duckdns.org" {
		t.Errorf("allowed hostnames = %v", cfg.Watcher.AllowedHostnames)
	}
}
