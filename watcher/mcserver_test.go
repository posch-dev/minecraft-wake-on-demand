package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/boot"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
)

// When the server is up, a status ping is answered by the server itself and
// not by the watcher's own MOTD.
func TestStatusPingIsProxiedWhenTheServerIsUp(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	cfg, waker := wakerFor(server)

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(mcproto.MakeStatusRequest()); err != nil {
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
	server := testsupport.StartFakeMCServer(t, false, reply)
	cfg, waker := wakerFor(server)

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	handshake := mcproto.MakeHandshake(770, "watcher.local", 25565, 2)
	if _, err := client.Write(handshake); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len(reply))
	client.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := testsupport.ReadFull(client, buf)
	if err != nil {
		t.Fatalf("the server's answer did not come back: %v", err)
	}
	if string(buf[:n]) != string(reply) {
		t.Errorf("got %q, want %q", buf[:n], reply)
	}

	got := server.Got()
	if len(got) < len(handshake) || string(got[:len(handshake)]) != string(handshake) {
		t.Errorf("the server received %d bytes, not the original handshake", len(got))
	}
}

func wakerFor(server *testsupport.FakeMCServer) (*config.Config, *boot.Waker) {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.Port
	cfg.Server.SSHUser = "tester"
	cfg.WoL.BroadcastAddress = "127.0.0.1"
	return &cfg, boot.NewWaker(&cfg)
}
