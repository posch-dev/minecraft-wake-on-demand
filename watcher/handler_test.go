package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// Port 1 on loopback refuses instantly, which is the sleeping server case
// without waiting for a timeout.
func sleepingConfig() *Config {
	cfg := defaultConfig()
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
			if length, off, err := readVarInt(buf, 0); err == nil {
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

func decodeStatus(t *testing.T, frame []byte) statusPayload {
	t.Helper()
	_, off, err := readVarInt(frame, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := readVarInt(frame, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("packet id = %d, err %v", pktID, err)
	}
	strLen, off, err := readVarInt(frame, off)
	if err != nil {
		t.Fatal(err)
	}
	var payload statusPayload
	if err := json.Unmarshal(frame[off:off+int(strLen)], &payload); err != nil {
		t.Fatalf("status payload is not JSON: %v", err)
	}
	return payload
}

func TestStatusPingWhileSleeping(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	if _, err := client.Write(buildHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(makeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	if payload.Players.Max != cfg.MOTD.MaxPlayers {
		t.Errorf("max players = %d, want %d", payload.Players.Max, cfg.MOTD.MaxPlayers)
	}
	if string(payload.Description) != defaultMOTDSleeping {
		t.Errorf("description = %s, want the sleeping MOTD", payload.Description)
	}
}

// The client is allowed to send everything in one write, which used to be a bug.
func TestStatusPingInOneSegment(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	combined := append(buildHandshake(770, "watcher.local", 25565, 1), makeStatusRequest()...)
	combined = append(combined, makePingResponse(1234567890)...)
	if _, err := client.Write(combined); err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, readFrame(t, client))
	if string(payload.Description) != defaultMOTDSleeping {
		t.Errorf("description = %s", payload.Description)
	}

	// The ping that came along has to be answered with the same payload.
	pong := readFrame(t, client)
	_, off, _ := readVarInt(pong, 0)
	pktID, off, err := readVarInt(pong, off)
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

	if _, err := client.Write(buildHandshake(770, "watcher.local", 25565, 2)); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, client)

	_, off, err := readVarInt(frame, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := readVarInt(frame, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("disconnect packet id = %d", pktID)
	}
	strLen, off, err := readVarInt(frame, off)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(frame[off : off+int(strLen)]); got != defaultMOTDLoginWait {
		t.Errorf("reason = %s", got)
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
	cfg.Transfer.LocalNetworks = StringList{"192.168.1.0/24"}
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

// TCP is free to split a write anywhere. The handler used to parse only what
// the first read returned, so a handshake arriving in pieces was dropped and
// the client saw the connection close with no answer.
func TestHandshakeSplitAcrossReads(t *testing.T) {
	cfg := sleepingConfig()
	h := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, h)

	combined := append(buildHandshake(770, "watcher.local", 25565, 1), makeStatusRequest()...)

	// One byte at a time is the worst case the handler has to survive.
	for i := 0; i < len(combined); i++ {
		if _, err := client.Write(combined[i : i+1]); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	payload := decodeStatus(t, readFrame(t, client))
	if string(payload.Description) != defaultMOTDSleeping {
		t.Errorf("description = %s, want the sleeping MOTD", payload.Description)
	}
}

// The same, with the handshake split at every possible offset.
func TestHandshakeSplitAtEveryOffset(t *testing.T) {
	handshake := buildHandshake(770, "watcher.local", 25565, 1)
	request := makeStatusRequest()

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
		if string(payload.Description) != defaultMOTDSleeping {
			t.Errorf("split %d: description = %s", split, payload.Description)
		}
		client.Close()
	}
}
