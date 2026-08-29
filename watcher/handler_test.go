package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// without waiting for a timeout.
func sleepingConfig() *config.Config {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = 1
	cfg.Server.SSHUser = "nobody"
	cfg.WoL.BroadcastAddress = "255.255.255.255"
	return &cfg
}

func serveOnce(t *testing.T, h *Handler) net.Conn {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		h.Handle(context.Background(), conn)
	}()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	client.SetDeadline(time.Now().Add(10 * time.Second))
	return client
}

func readFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	buf := make([]byte, 0, readBufferSize)
	chunk := make([]byte, readBufferSize)
	for {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if length, off, err := mcproto.ReadVarInt(buf, 0); err == nil {
				if len(buf) >= off+int(length) {
					return buf[:off+int(length)]
				}
			}
		}
		if err != nil {
			t.Fatalf("read failed after %d bytes: %v", len(buf), err)
		}
	}
}

func decodeStatus(t *testing.T, frame []byte) mcproto.StatusPayload {
	t.Helper()
	_, off, err := mcproto.ReadVarInt(frame, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := mcproto.ReadVarInt(frame, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("packet id = %d, err %v", pktID, err)
	}
	strLen, off, err := mcproto.ReadVarInt(frame, off)
	if err != nil {
		t.Fatal(err)
	}
	var payload mcproto.StatusPayload
	if err := json.Unmarshal(frame[off:off+int(strLen)], &payload); err != nil {
		t.Fatalf("status payload is not JSON: %v", err)
	}
	return payload
}

func TestStatusPingWhileSleeping(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(mcproto.MakeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	if payload.Players.Max != cfg.MOTD.MaxPlayers {
		t.Errorf("max players = %d, want %d", payload.Players.Max, cfg.MOTD.MaxPlayers)
	}
	if string(payload.Description) != config.DefaultMOTDSleeping {
		t.Errorf("description = %s, want the sleeping MOTD", payload.Description)
	}
}

// The client is allowed to send everything in one write, which used to be a bug.
func TestStatusPingInOneSegment(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	combined := append(mcproto.MakeHandshake(770, "watcher.local", 25565, 1), mcproto.MakeStatusRequest()...)
	combined = append(combined, mcproto.MakePingResponse(1234567890)...)
	if _, err := client.Write(combined); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	if string(payload.Description) != config.DefaultMOTDSleeping {
		t.Errorf("description = %s", payload.Description)
	}

	// The ping that came along has to be answered with the same payload.
	pong := readFrame(t, client)
	_, off, _ := mcproto.ReadVarInt(pong, 0)
	pktID, off, err := mcproto.ReadVarInt(pong, off)
	if err != nil || pktID != 0x01 {
		t.Fatalf("pong packet id = %d", pktID)
	}
	if len(pong) < off+8 {
		t.Fatal("pong is too short")
	}
}

func TestLoginWhileSleepingSendsWaitMessage(t *testing.T) {
	cfg := sleepingConfig()
	// Keep the boot attempt from actually sending WoL and waiting for SSH.
	cfg.Limits.BootCooldown = 3600
	waker := NewWaker(cfg)
	waker.lastAttempt = time.Now()

	h := NewHandler(cfg, waker)
	client := serveOnce(t, h)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 2)); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, client)

	_, off, err := mcproto.ReadVarInt(frame, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := mcproto.ReadVarInt(frame, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("disconnect packet id = %d", pktID)
	}
	strLen, off, err := mcproto.ReadVarInt(frame, off)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(frame[off : off+int(strLen)]); got != config.DefaultMOTDLoginWait {
		t.Errorf("reason = %s", got)
	}
}

func TestStatusPingWhileSleepingShowsCachedInfo(t *testing.T) {
	dir := t.TempDir()
	cfg := sleepingConfig()
	cfg.Path = filepath.Join(dir, "config.yml")

	cachePath := filepath.Join(dir, ".server-info.json")
	sv := &ServerInfo{Name: "1.21.4", Protocol: 769, Updated: time.Now()}
	data, _ := json.Marshal(map[string]*ServerInfo{"default": sv})
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	waker := NewWaker(cfg)
	h := NewHandler(cfg, waker)
	client := serveOnce(t, h)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(mcproto.MakeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	if payload.Version.Name != "1.21.4" {
		t.Errorf("version name = %q, want 1.21.4", payload.Version.Name)
	}
	if payload.Version.Protocol != 769 {
		t.Errorf("version protocol = %d, want 769", payload.Version.Protocol)
	}
}

func TestIsLocalClient(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))

	local := []string{"192.168.1.20:5000", "10.1.2.3:5000", "127.0.0.1:5000", "172.16.0.5:5000"}
	for _, a := range local {
		addr, _ := net.ResolveTCPAddr("tcp", a)
		if !h.isLocalClient(addr) {
			t.Errorf("%s should count as local", a)
		}
	}
	remote := []string{"8.8.8.8:5000", "203.0.113.7:5000"}
	for _, a := range remote {
		addr, _ := net.ResolveTCPAddr("tcp", a)
		if h.isLocalClient(addr) {
			t.Errorf("%s should not count as local", a)
		}
	}
}

func TestIsLocalClientHonoursConfiguredNetworks(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Transfer.LocalNetworks = config.StringList{"192.168.1.0/24"}
	h := NewHandler(cfg, NewWaker(cfg))

	inside, _ := net.ResolveTCPAddr("tcp", "192.168.1.50:5000")
	if !h.isLocalClient(inside) {
		t.Error("192.168.1.50 is inside the configured network")
	}
	// A private address outside the list is no longer local.
	outside, _ := net.ResolveTCPAddr("tcp", "10.0.0.5:5000")
	if h.isLocalClient(outside) {
		t.Error("10.0.0.5 is outside the configured network")
	}
}

// TCP may split a write anywhere, and only the first read used to be parsed.
// A handshake arriving in pieces was dropped with no answer at all.
func TestHandshakeSplitAcrossReads(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	combined := append(mcproto.MakeHandshake(770, "watcher.local", 25565, 1), mcproto.MakeStatusRequest()...)

	// One byte at a time is the worst case the handler has to survive.
	for i := 0; i < len(combined); i++ {
		if _, err := client.Write(combined[i : i+1]); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	payload := decodeStatus(t, readFrame(t, client))
	if string(payload.Description) != config.DefaultMOTDSleeping {
		t.Errorf("description = %s, want the sleeping MOTD", payload.Description)
	}
}

// The same, with the handshake split at every possible offset.
func TestHandshakeSplitAtEveryOffset(t *testing.T) {
	handshake := mcproto.MakeHandshake(770, "watcher.local", 25565, 1)
	request := mcproto.MakeStatusRequest()

	for split := 1; split < len(handshake); split++ {
		cfg := sleepingConfig()
		h := NewHandler(cfg, NewWaker(cfg))
		client := serveOnce(t, h)

		if _, err := client.Write(handshake[:split]); err != nil {
			t.Fatalf("split %d, first write: %v", split, err)
		}
		rest := append(append([]byte{}, handshake[split:]...), request...)
		if _, err := client.Write(rest); err != nil {
			t.Fatalf("split %d, second write: %v", split, err)
		}

		payload := decodeStatus(t, readFrame(t, client))
		if string(payload.Description) != config.DefaultMOTDSleeping {
			t.Errorf("split %d: description = %s", split, payload.Description)
		}
		client.Close()
	}
}

func TestAllowedHostnamesEmptyPermitsAll(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	hs := &mcproto.Handshake{ServerAddress: "1.2.3.4", NextState: 1}
	if !h.isAllowedHostname(hs, remote) {
		t.Error("empty allowed_hostnames should permit all connections")
	}
}

func TestAllowedHostnamesBlocksUnknownRemote(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"mc.example.org"}
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	hs := &mcproto.Handshake{ServerAddress: "1.2.3.4", NextState: 1}
	if h.isAllowedHostname(hs, remote) {
		t.Error("remote IP not in allowed list should be blocked")
	}
}

func TestAllowedHostnamesAllowsMatchingRemote(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"mc.example.org", "192.168.1.100"}
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	hs := &mcproto.Handshake{ServerAddress: "mc.example.org", NextState: 1}
	if !h.isAllowedHostname(hs, remote) {
		t.Error("hostname match from remote IP should be allowed")
	}
}

func TestAllowedHostnamesLocalBypassesCheck(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"mc.example.org"}
	h := NewHandler(cfg, NewWaker(cfg))

	loopback, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:5000")
	hs := &mcproto.Handshake{ServerAddress: "scanner.ip", NextState: 1}
	if !h.isAllowedHostname(hs, loopback) {
		t.Error("local client should bypass hostname check")
	}
}

func TestAllowedHostnamesMatchIsCaseInsensitive(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"MC.Example.Org"}
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	hs := &mcproto.Handshake{ServerAddress: "mc.example.org", NextState: 1}
	if !h.isAllowedHostname(hs, remote) {
		t.Error("hostname matching should be case-insensitive")
	}
}

func TestAllowedHostnamesAutoPopulatedFromDuckDNS(t *testing.T) {
	cfg := sleepingConfig()
	cfg.DuckDNS.Enabled = true
	cfg.DuckDNS.Domain = "my-world"
	cfg.DuckDNS.Token = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(cfg.Watcher.AllowedHostnames) != 1 {
		t.Fatalf("expected 1 auto-populated hostname, got %d", len(cfg.Watcher.AllowedHostnames))
	}
	if cfg.Watcher.AllowedHostnames[0] != "my-world.duckdns.org" {
		t.Errorf("expected my-world.duckdns.org, got %q", cfg.Watcher.AllowedHostnames[0])
	}

	h := NewHandler(cfg, NewWaker(cfg))
	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	hs := &mcproto.Handshake{ServerAddress: "my-world.duckdns.org", NextState: 1}
	if !h.isAllowedHostname(hs, remote) {
		t.Error("auto-populated DuckDNS domain should be accepted")
	}
}

func TestAllowedHostnamesIgnoreForgeMarker(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"mc.example.org"}
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	for _, address := range []string{
		"mc.example.org\x00FML3\x00",
		"mc.example.org\x00FML\x00",
		"mc.example.org\x00203.0.113.7\x0069e8e2ee\x00[]",
		"mc.example.org.",
	} {
		hs := &mcproto.Handshake{ServerAddress: address, NextState: 1}
		if !h.isAllowedHostname(hs, remote) {
			t.Errorf("address %q should be accepted", address)
		}
	}
}

func TestAllowedHostnamesStillBlockOtherNames(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Watcher.AllowedHostnames = config.StringList{"mc.example.org"}
	h := NewHandler(cfg, NewWaker(cfg))

	remote, _ := net.ResolveTCPAddr("tcp", "8.8.8.8:5000")
	for _, address := range []string{
		"evil.example.org\x00FML3\x00",
		"\x00mc.example.org",
		"mc.example.org.evil.net",
		"",
	} {
		hs := &mcproto.Handshake{ServerAddress: address, NextState: 1}
		if h.isAllowedHostname(hs, remote) {
			t.Errorf("address %q should be blocked", address)
		}
	}
}

func TestNormalizeServerAddress(t *testing.T) {
	cases := map[string]string{
		"mc.example.org":             "mc.example.org",
		"mc.example.org.":            "mc.example.org",
		"mc.example.org\x00FML3\x00": "mc.example.org",
		"  mc.example.org  ":         "mc.example.org",
		"\x00":                       "",
	}
	for in, want := range cases {
		if got := normalizeServerAddress(in); got != want {
			t.Errorf("normalizeServerAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
