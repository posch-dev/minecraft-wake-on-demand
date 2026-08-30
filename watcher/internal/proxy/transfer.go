package proxy

import (
	"context"
	"net"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// Client receives a transfer packet, traffic skips the watcher.
func (h *Handler) handleTransfer(ctx context.Context, conn net.Conn, initial []byte, hs *mcproto.Handshake, addr net.Addr) {
	loginData := trailing(initial, hs.End)
	if len(loginData) == 0 {
		conn.SetReadDeadline(time.Now().Add(loginReadTimeout))
		buf := make([]byte, readBufferSize)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			logging.Errorf("Transfer failed for %s: no login packet", addr)
			return
		}
		loginData = buf[:n]
	}

	name, uuid, err := mcproto.ParseLoginStart(loginData)
	if err != nil {
		logging.Warnf("Failed to parse login start from %s: %v", addr, err)
		return
	}

	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	if _, err := conn.Write(mcproto.MakeLoginSuccess(uuid, name, hs.ProtocolVersion)); err != nil {
		logging.Errorf("Transfer failed for %s: %v", addr, err)
		return
	}

	// Wait for login acknowledged packet: client moves to configuration state, where transfer is valid.
	conn.SetReadDeadline(time.Now().Add(statusTimeout))
	ack := make([]byte, readBufferSize)
	if _, err := conn.Read(ack); err != nil {
		logging.Errorf("Transfer failed for %s: %v", addr, err)
		return
	}

	targetHost, targetPort := h.cfg.Transfer.Host, h.cfg.Transfer.Port
	if h.isLocalClient(addr) {
		targetHost, targetPort = h.cfg.Server.IP, h.cfg.Server.MCPort
	}

	logging.Infof("Transferring %s to %s:%d", logging.Sanitize(name, 64), targetHost, targetPort)
	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	conn.Write(mcproto.MakeTransferPacket(targetHost, targetPort))
}

// Players on the LAN cannot reach the public host unless the router does NAT
// loopback, so they are sent to the server directly instead.
func (h *Handler) isLocalClient(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if len(h.localNets) > 0 {
		for _, n := range h.localNets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}
