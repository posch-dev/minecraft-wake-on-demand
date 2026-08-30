// Wake-on-LAN: the magic packet and the two ways of addressing it.
package wol

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// The port nothing listens on, which is the point: the card reads the packet
// before the machine is awake enough to have a socket.
const Port = 9

// Six 0xFF bytes followed by the MAC sixteen times, 102 bytes in total.
func MagicPacket(mac string) ([]byte, error) {
	parsed, err := config.ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	payload := bytes.Repeat([]byte{0xFF}, 6)
	for i := 0; i < 16; i++ {
		payload = append(payload, parsed...)
	}
	return payload, nil
}

func Send(cfg *config.Config, port int) error {
	payload, err := MagicPacket(cfg.Server.MAC)
	if err != nil {
		return err
	}

	target := cfg.Server.IP
	broadcast := cfg.WoL.Mode == "broadcast"
	if broadcast {
		target = cfg.WoL.BroadcastAddress
	}

	dialer := net.Dialer{Timeout: 5 * time.Second}
	if broadcast {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) { sockErr = setBroadcast(fd) }); err != nil {
				return err
			}
			return sockErr
		}
	}

	conn, err := dialer.Dial("udp", net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("cannot open WoL socket: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("cannot send WoL packet: %w", err)
	}
	logging.Infof("WoL magic packet sent to %s (%s mode)", target, cfg.WoL.Mode)
	return nil
}
