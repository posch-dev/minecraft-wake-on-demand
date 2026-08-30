package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// A live server answers for itself. If it does not, the watcher answers rather
// than leaving the entry in the server list looking broken.
func (h *Handler) handleStatus(ctx context.Context, conn net.Conn, initial []byte, hs *mcproto.Handshake) {
	// Clients can pack handshake, status request and ping into one segment or
	// send them apart. Both paths below need the request, so it is read once
	// here rather than by whichever path happens to run.
	rest := trailing(initial, hs.End)
	if len(rest) == 0 {
		conn.SetReadDeadline(time.Now().Add(statusTimeout))
		buf := make([]byte, readBufferSize)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		rest = buf[:n]
	}

	if h.waker.MCPortReachable(ctx, false) && h.proxyStatus(ctx, conn, initial, rest) {
		return
	}
	h.answerStatus(ctx, conn, rest, hs)
}

func (h *Handler) answerStatus(ctx context.Context, conn net.Conn, rest []byte, hs *mcproto.Handshake) {
	motd, icon := h.assets.MOTDSleeping(), h.assets.IconSleeping()
	if h.waker.Booting() {
		motd, icon = h.assets.MOTDStarting(), h.assets.IconStarting()
	}

	// Whatever follows the status request is the ping the client sent along.
	var pingData []byte
	if reqLen, off, err := mcproto.ReadVarInt(rest, 0); err == nil {
		if end := off + int(reqLen); end >= 0 && end <= len(rest) {
			pingData = rest[end:]
		}
	}

	// Cached version if available, else echo client protocol so signal bars stay green.
	versionName := ""
	versionProtocol := int(hs.ProtocolVersion)
	if info := h.waker.CachedInfo(); info != nil {
		versionName = info.Name
		versionProtocol = info.Protocol
	}

	maxPlayers := h.cfg.MOTD.MaxPlayers
	if info := h.waker.CachedInfo(); info != nil && info.MaxPlayers > 0 {
		maxPlayers = info.MaxPlayers
	}

	response, err := mcproto.MakeStatusResponse(motd, maxPlayers, 0, icon, versionName, versionProtocol)
	if err != nil {
		logging.Errorf("Cannot build status response: %v", err)
		return
	}
	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	if _, err := conn.Write(response); err != nil {
		return
	}

	if len(pingData) == 0 {
		conn.SetReadDeadline(time.Now().Add(statusTimeout))
		buf := make([]byte, readBufferSize)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		pingData = buf[:n]
	}
	if len(pingData) < 10 {
		return
	}
	_, off, err := mcproto.ReadVarInt(pingData, 0)
	if err != nil {
		return
	}
	_, off, err = mcproto.ReadVarInt(pingData, off)
	if err != nil || off+8 > len(pingData) {
		return
	}
	payload := int64(binary.BigEndian.Uint64(pingData[off : off+8]))
	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	conn.Write(mcproto.MakePingResponse(payload))
}

// Forwards the ping to the real server, then swaps in the watcher's own MOTD
// and icon if either is overridden. Player count and version stay real.
// False means nothing reached the client, so the caller can still answer.
func (h *Handler) proxyStatus(ctx context.Context, conn net.Conn, initial, rest []byte) bool {
	target := net.JoinHostPort(h.cfg.Server.IP, strconv.Itoa(h.cfg.Server.MCPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	server, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		logging.Warnf("Cannot reach %s for a status ping: %v", target, err)
		return false
	}
	defer server.Close()

	server.SetDeadline(time.Now().Add(statusTimeout))
	if _, err := server.Write(initial); err != nil {
		return false
	}
	// The server says nothing until it has the request as well, and a client
	// that sent it in its own packet is why this used to time out.
	if !bytes.HasSuffix(initial, rest) {
		if _, err := server.Write(rest); err != nil {
			return false
		}
	}
	body, err := mcproto.ReadFramedPacket(server, mcproto.MaxStatusResponseBytes)
	if err != nil {
		logging.Warnf("Cannot read the status response from %s: %v", target, err)
		return false
	}

	response, err := h.dressStatusResponse(body)
	if err != nil {
		logging.Warnf("Cannot rebuild the status response from %s: %v", target, err)
		return false
	}

	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	if _, err := conn.Write(response); err != nil {
		return true
	}
	h.answerPing(conn, server)
	return true
}

// Without an override the server's own answer is passed on byte for byte.
func (h *Handler) dressStatusResponse(body []byte) ([]byte, error) {
	motd, icon := h.assets.MOTDLive(), h.assets.IconLive()
	if motd == "" && icon == "" {
		return mcproto.FramePacket(body), nil
	}
	return mcproto.RewriteStatusResponse(body, motd, icon)
}

// The client's ping payload has to come back unchanged, so it is relayed rather
// than parsed.
func (h *Handler) answerPing(client, server net.Conn) {
	client.SetReadDeadline(time.Now().Add(statusTimeout))
	buf := make([]byte, readBufferSize)
	n, err := client.Read(buf)
	if err != nil || n == 0 {
		return
	}

	server.SetDeadline(time.Now().Add(statusTimeout))
	if _, err := server.Write(buf[:n]); err != nil {
		return
	}
	pong := make([]byte, readBufferSize)
	n, err = server.Read(pong)
	if err != nil || n == 0 {
		return
	}
	client.SetWriteDeadline(time.Now().Add(statusTimeout))
	client.Write(pong[:n])
}
