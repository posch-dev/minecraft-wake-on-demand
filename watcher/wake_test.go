package main

import (
	"bytes"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestBuildMagicPacket(t *testing.T) {
	packet, err := buildMagicPacket("01:23:45:67:89:AB")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 102 {
		t.Fatalf("packet is %d bytes, want 102", len(packet))
	}
	if !bytes.Equal(packet[:6], []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Errorf("header = % x", packet[:6])
	}
	mac := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB}
	for i := 0; i < 16; i++ {
		start := 6 + i*6
		if !bytes.Equal(packet[start:start+6], mac) {
			t.Fatalf("repetition %d = % x", i, packet[start:start+6])
		}
	}

	// The separators must not change the result.
	for _, form := range []string{"01-23-45-67-89-ab", "0123456789ab"} {
		other, err := buildMagicPacket(form)
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		if !bytes.Equal(other, packet) {
			t.Errorf("%s produced a different packet", form)
		}
	}
	if _, err := buildMagicPacket("nonsense"); err == nil {
		t.Error("an invalid MAC must not produce a packet")
	}
}

func listenUDP(t *testing.T) (*net.UDPConn, int) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, portString, _ := net.SplitHostPort(conn.LocalAddr().String())
	port, _ := strconv.Atoi(portString)
	return conn, port
}

func TestSendMagicPacketReachesTheWire(t *testing.T) {
	for _, mode := range []string{"unicast", "broadcast"} {
		t.Run(mode, func(t *testing.T) {
			listener, port := listenUDP(t)

			cfg := defaultConfig()
			cfg.Server.MAC = "01:23:45:67:89:AB"
			cfg.Server.IP = "127.0.0.1"
			cfg.Server.SSHUser = "tester"
			cfg.WoL.Mode = mode
			// Both modes point at the listener, so the socket option is the
			// only difference under test.
			cfg.WoL.BroadcastAddress = "127.0.0.1"

			waker := NewWaker(&cfg)
			waker.wolPort = port

			if err := waker.SendMagicPacket(); err != nil {
				t.Fatalf("SendMagicPacket: %v", err)
			}

			listener.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 200)
			n, _, err := listener.ReadFrom(buf)
			if err != nil {
				t.Fatalf("nothing arrived: %v", err)
			}
			want, _ := buildMagicPacket(cfg.Server.MAC)
			if !bytes.Equal(buf[:n], want) {
				t.Errorf("received %d bytes, not the magic packet", n)
			}
		})
	}
}

func TestCooldownWithoutFailuresUsesTheShortGap(t *testing.T) {
	cfg := defaultConfig()
	cfg.Limits.BootCooldown = 10
	waker := NewWaker(&cfg)

	if waker.cooldownRemaining() != 0 {
		t.Error("a watcher that never booted must not be in cooldown")
	}

	waker.lastAttempt = time.Now()
	remaining := waker.cooldownRemaining()
	if remaining <= 0 || remaining > 10*time.Second {
		t.Errorf("remaining = %v, want just under 10s", remaining)
	}
}

// The gap doubles per failure and stops at boot_max_backoff.
func TestFailureBackoffDoublesAndIsCapped(t *testing.T) {
	cfg := defaultConfig()
	cfg.Limits.BootCooldown = 10
	cfg.Limits.BootFailureBackoff = 60
	cfg.Limits.BootMaxBackoff = 900
	waker := NewWaker(&cfg)
	waker.lastAttempt = time.Now()

	expected := map[int]time.Duration{
		1: 60 * time.Second,
		2: 120 * time.Second,
		3: 240 * time.Second,
		4: 480 * time.Second,
		5: 900 * time.Second, // 960 would exceed the cap
		6: 900 * time.Second,
	}
	for failures, want := range expected {
		waker.failures = failures
		got := waker.cooldownRemaining()
		// Allow for the time the test itself takes.
		if got > want || got < want-2*time.Second {
			t.Errorf("%d failures gave %v, want about %v", failures, got, want)
		}
	}
}

// The Python version relied on arbitrary precision integers here, a fixed
// width shift would overflow into a negative delay.
func TestBackoffSurvivesAbsurdFailureCounts(t *testing.T) {
	cfg := defaultConfig()
	cfg.Limits.BootFailureBackoff = 60
	cfg.Limits.BootMaxBackoff = 900
	waker := NewWaker(&cfg)
	waker.lastAttempt = time.Now()

	for _, failures := range []int{30, 31, 64, 1000, 100000} {
		waker.failures = failures
		got := waker.cooldownRemaining()
		if got <= 0 {
			t.Errorf("%d failures gave %v, the gap must never collapse", failures, got)
		}
		if got > 900*time.Second {
			t.Errorf("%d failures gave %v, above the cap", failures, got)
		}
	}
}

func TestBootStateTracksFailuresAndResets(t *testing.T) {
	cfg := defaultConfig()
	waker := NewWaker(&cfg)

	if waker.Booting() {
		t.Error("a fresh watcher is not booting")
	}
	waker.beginBoot()
	if !waker.Booting() {
		t.Error("beginBoot should mark it as booting")
	}
	waker.endBoot(false)
	if waker.Booting() {
		t.Error("endBoot should clear the flag")
	}
	if waker.failures != 1 {
		t.Errorf("failures = %d, want 1", waker.failures)
	}

	waker.beginBoot()
	waker.endBoot(false)
	if waker.failures != 2 {
		t.Errorf("failures = %d, want 2", waker.failures)
	}

	waker.beginBoot()
	waker.endBoot(true)
	if waker.failures != 0 {
		t.Errorf("a success must reset the counter, got %d", waker.failures)
	}
}
