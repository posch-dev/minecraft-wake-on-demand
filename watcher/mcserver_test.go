package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Stands in for the real Minecraft server so the paths that only run when the
// server is up can be exercised.
type fakeMCServer struct {
	port     int
	mu       sync.Mutex
	received []byte
}

// answerStatus replies to a status request the way a running server would.
// When false the connection is accepted and then left silent, which is what a
// container looks like while it is still starting.
func startFakeMCServer(t *testing.T, answerStatus bool, echo []byte) *fakeMCServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	server := &fakeMCServer{port: port}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 4096)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				server.mu.Lock()
				server.received = append(server.received, buf[:n]...)
				server.mu.Unlock()

				if answerStatus {
					motd := "{\"text\":\"the real server\",\"color\":\"green\"}"
					response, _ := makeStatusResponse(motd, 42, 7, "", "1.21.4", 769)
					conn.Write(response)
				}
				if echo != nil {
					conn.Write(echo)
				}
				if answerStatus || echo != nil {
					// Closing straight after the write races the proxy copying
					// it on, so the peer is left to hang up first.
					conn.Read(buf)
				}
				if !answerStatus && echo == nil {
					// Hold the connection open without ever answering.
					time.Sleep(6 * time.Second)
				}
			}()
		}
	}()
	return server
}

func (s *fakeMCServer) got() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.received...)
}

func wakerFor(server *fakeMCServer) (*Config, *Waker) {
	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.port
	cfg.Server.SSHUser = "tester"
	cfg.WoL.BroadcastAddress = "127.0.0.1"
	return &cfg, NewWaker(&cfg)
}

func TestMCAcceptsStatusAgainstARunningServer(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	cfg, waker := wakerFor(server)
	_ = cfg

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("a server that answers a status request counts as ready")
	}

	// It has to send a well formed handshake, not just open a socket.
	handshake, err := parseHandshake(server.got())
	if err != nil {
		t.Fatalf("the probe sent something unparseable: %v", err)
	}
	if handshake.NextState != 1 {
		t.Errorf("next state = %d, want 1 for a status probe", handshake.NextState)
	}
}

// An open port is not enough, this is the case the probe exists for.
func TestMCAcceptsStatusRejectsASilentPort(t *testing.T) {
	server := startFakeMCServer(t, false, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	if waker.mcAcceptsStatus(ctx) {
		t.Fatal("a port that never answers must not count as ready")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("the probe took %v, it has to give up on its own deadline", elapsed)
	}
}

func TestMCPortReachableIsCached(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.MCPortReachable(ctx, false) {
		t.Fatal("the fake server is listening")
	}
	checkedAt := waker.reachChecked

	// A second call inside the TTL must not probe again.
	if !waker.MCPortReachable(ctx, false) {
		t.Fatal("the cached answer should still be true")
	}
	if !waker.reachChecked.Equal(checkedAt) {
		t.Error("the second call probed again instead of using the cache")
	}

	// force has to bypass it, which is what the boot sequence relies on. The
	// cache is pushed into the past first, because two calls microseconds
	// apart can read the same wall clock on a coarse timer.
	waker.reachChecked = time.Now().Add(-time.Hour)
	stale := waker.reachChecked
	if !waker.MCPortReachable(ctx, true) {
		t.Fatal("forced probe should still find the server")
	}
	if !waker.reachChecked.After(stale) {
		t.Error("force did not refresh the cache")
	}
}

// When the server is up, a status ping is answered by the server itself and
// not by the watcher's own MOTD.
func TestStatusPingIsProxiedWhenTheServerIsUp(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	cfg, waker := wakerFor(server)

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	if _, err := client.Write(buildHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(makeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	var description struct {
		Text string `json:"text"`
	}
	json.Unmarshal(payload.Description, &description)
	if description.Text != "the real server" {
		t.Errorf("description = %s, want the server's own MOTD", payload.Description)
	}
	if payload.Players.Online != 7 || payload.Players.Max != 42 {
		t.Errorf("players = %+v, want the server's own numbers", payload.Players)
	}
}

// The handshake the client sent has to reach the server unchanged, otherwise
// the server sees a truncated login.
func TestProxyForwardsTheHandshakeAndTheAnswer(t *testing.T) {
	reply := []byte("HELLO-FROM-THE-SERVER")
	server := startFakeMCServer(t, false, reply)
	cfg, waker := wakerFor(server)

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	handshake := buildHandshake(770, "watcher.local", 25565, 2)
	if _, err := client.Write(handshake); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len(reply))
	client.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := readFull(client, buf)
	if err != nil {
		t.Fatalf("the server's answer did not come back: %v", err)
	}
	if string(buf[:n]) != string(reply) {
		t.Errorf("got %q, want %q", buf[:n], reply)
	}

	got := server.got()
	if len(got) < len(handshake) || string(got[:len(handshake)]) != string(handshake) {
		t.Errorf("the server received %d bytes, not the original handshake", len(got))
	}
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func TestSaveAndLoadServerInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")

	waker := NewWaker(&cfg)
	sv := &ServerInfo{Name: "1.21.4", Protocol: 769, Updated: time.Now()}
	waker.saveServerInfo(sv)

	waker2 := NewWaker(&cfg)
	cached := waker2.CachedInfo()
	if cached == nil {
		t.Fatal("cached version was not loaded")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v", cached)
	}
}

func TestLoadServerInfoNoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")

	waker := NewWaker(&cfg)
	if waker.CachedInfo() != nil {
		t.Error("expected nil version when no cache file exists")
	}
}

func TestLoadServerInfoCorruptFile(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, ".server-info.json")
	if err := os.WriteFile(cachePath, []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")

	waker := NewWaker(&cfg)
	if waker.CachedInfo() != nil {
		t.Error("expected nil version when cache file is corrupt")
	}
}

func TestLearnServerInfoCachesInMemory(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	cached := waker.CachedInfo()
	if cached == nil {
		t.Fatal("version was not learned from the status response")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v, want name=1.21.4 protocol=769", cached)
	}
}

func TestLearnServerInfoIgnoresEmptyVersion(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	waker.infoMu.Lock()
	waker.info = nil
	waker.infoMu.Unlock()

	response, _ := makeStatusResponse("{\"text\":\"x\"}", 10, 0, "", "", 0)
	waker.learnServerInfo(statusBody(t, response))

	if waker.CachedInfo() != nil {
		t.Error("empty version should not be cached")
	}
}

func TestLearnServerInfoDoesNotUpdateWhenSame(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	first := waker.CachedInfo()
	if first == nil {
		t.Fatal("version should have been learned")
	}

	// Same version and same player slots as the fake server reported.
	response, _ := makeStatusResponse("{\"text\":\"x\"}", 42, 0, "", "1.21.4", 769)
	waker.learnServerInfo(statusBody(t, response))

	cached := waker.CachedInfo()
	if !cached.Updated.Equal(first.Updated) {
		t.Error("learning the same info should not bump Updated")
	}
}

func TestLearnServerInfoPicksUpChangedPlayerSlots(t *testing.T) {
	server := startFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}
	if got := waker.CachedInfo().MaxPlayers; got != 42 {
		t.Fatalf("max players = %d, want 42", got)
	}

	response, _ := makeStatusResponse("{\"text\":\"x\"}", 60, 0, "", "1.21.4", 769)
	waker.learnServerInfo(statusBody(t, response))

	if got := waker.CachedInfo().MaxPlayers; got != 60 {
		t.Errorf("max players = %d, want 60 after the server raised the slots", got)
	}
}

func TestServerInfoCachesToFile(t *testing.T) {
	dir := t.TempDir()
	server := startFakeMCServer(t, true, nil)
	cfg := defaultConfig()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.port
	cfg.Server.SSHUser = "tester"
	cfg.WoL.BroadcastAddress = "127.0.0.1"
	cfg.Path = filepath.Join(dir, "config.yml")
	waker := NewWaker(&cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	time.Sleep(100 * time.Millisecond)

	waker2 := NewWaker(&cfg)
	cached := waker2.CachedInfo()
	if cached == nil {
		t.Fatal("version was not persisted to disk")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v, want name=1.21.4 protocol=769", cached)
	}
}

// learnServerInfo works on the packet body, the test helpers build whole frames.
func statusBody(t *testing.T, frame []byte) []byte {
	t.Helper()
	body, err := readFramedPacket(bytes.NewReader(frame), maxStatusResponseBytes)
	if err != nil {
		t.Fatalf("readFramedPacket: %v", err)
	}
	return body
}

// Same fake, but the status response carries a favicon.
func startFakeMCServerWithIcon(t *testing.T, favicon string) *fakeMCServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	server := &fakeMCServer{port: port}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 4096)
				if _, err := conn.Read(buf); err != nil {
					return
				}
				response, _ := makeStatusResponse(defaultMOTDSleeping, 20, 0, favicon, "1.21.4", 769)
				conn.Write(response)
				conn.Read(buf)
			}()
		}
	}()
	return server
}
