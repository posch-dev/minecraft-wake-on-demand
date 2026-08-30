package wol

import (
	"bytes"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func TestSendMagicPacketReachesTheWire(t *testing.T) {
	for _, mode := range []string{"unicast", "broadcast"} {
		t.Run(mode, func(t *testing.T) {
			listener, port := listenUDP(t)

			cfg := config.Default()
			cfg.Server.MAC = "01:23:45:67:89:AB"
			cfg.Server.IP = "127.0.0.1"
			cfg.Server.SSHUser = "tester"
			cfg.WoL.Mode = mode
			// Both modes point at the listener, so the socket option is the
			// only difference under test.
			cfg.WoL.BroadcastAddress = "127.0.0.1"

			if err := Send(&cfg, port); err != nil {
				t.Fatalf("SendMagicPacket: %v", err)
			}

			listener.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 200)
			n, _, err := listener.ReadFrom(buf)
			if err != nil {
				t.Fatalf("nothing arrived: %v", err)
			}
			want, _ := MagicPacket(cfg.Server.MAC)
			if !bytes.Equal(buf[:n], want) {
				t.Errorf("received %d bytes, not the magic packet", n)
			}
		})
	}
}

func TestMagicPacket(t *testing.T) {
	packet, err := MagicPacket("01:23:45:67:89:AB")
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
		other, err := MagicPacket(form)
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		if !bytes.Equal(other, packet) {
			t.Errorf("%s produced a different packet", form)
		}
	}
	if _, err := MagicPacket("nonsense"); err == nil {
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
