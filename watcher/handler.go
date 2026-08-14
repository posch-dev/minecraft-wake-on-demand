package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	readBufferSize   = 4096
	handshakeTimeout = 10 * time.Second
	statusTimeout    = 5 * time.Second
	loginReadTimeout = 10 * time.Second
)

type Handler struct {
	cfg       *Config
	waker     *Waker
	assets    *Assets
	localNets []*net.IPNet
}

func NewHandler(cfg *Config, waker *Waker) *Handler {
	return &Handler{
		cfg:       cfg,
		waker:     waker,
		assets:    NewAssets(cfg),
		localNets: cfg.ParsedLocalNetworks(),
	}
}

func (h *Handler) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr()

	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	buf := make([]byte, readBufferSize)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return
	}
	initial := buf[:n]

	handshake, err := parseHandshake(initial)
	if err != nil {
		return
	}

	switch handshake.NextState {
	case 1:
		h.handleStatus(ctx, conn, initial, handshake)
	case 2:
		h.handleLogin(ctx, conn, initial, handshake, addr)
	}
}

func (h *Handler) handleStatus(ctx context.Context, conn net.Conn, initial []byte, hs *Handshake) {
	if h.waker.MCPortReachable(ctx, false) {
		h.proxy(ctx, conn, initial)
		return
	}

	motd := h.assets.MOTDSleeping()
	if h.waker.Booting() {
		motd = h.assets.MOTDStarting()
	}

	// Clients are free to pack handshake, status request and ping into one
	// segment, so only read again when nothing followed the handshake.
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

	// Whatever follows the status request is the ping the client sent along.
	var pingData []byte
	if reqLen, off, err := readVarInt(rest, 0); err == nil {
		if end := off + int(reqLen); end >= 0 && end <= len(rest) {
			pingData = rest[end:]
		}
	}

	response, err := makeStatusResponse(motd, h.cfg.MOTD.MaxPlayers, 0, h.assets.Icon())
	if err != nil {
		log.Errorf("Cannot build status response: %v", err)
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
	_, off, err := readVarInt(pingData, 0)
	if err != nil {
		return
	}
	_, off, err = readVarInt(pingData, off)
	if err != nil || off+8 > len(pingData) {
		return
	}
	payload := int64(binary.BigEndian.Uint64(pingData[off : off+8]))
	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	conn.Write(makePingResponse(payload))
}

func (h *Handler) handleLogin(ctx context.Context, conn net.Conn, initial []byte, hs *Handshake, addr net.Addr) {
	log.Infof("Login attempt from %s", addr)

	if !h.waker.MCPortReachable(ctx, false) {
		if !h.waker.FullBoot(ctx) {
			log.Infof("Server not ready for %s, sending wait message", addr)
			conn.SetWriteDeadline(time.Now().Add(statusTimeout))
			conn.Write(makeLoginDisconnect(h.cfg.MOTD.LoginWait))
			return
		}
	}

	if h.cfg.Transfer.Enabled {
		h.handleTransfer(ctx, conn, initial, hs, addr)
		return
	}
	h.proxy(ctx, conn, initial)
}

// The client is accepted into the login state and then handed a transfer
// packet, so the traffic never flows through the watcher.
func (h *Handler) handleTransfer(ctx context.Context, conn net.Conn, initial []byte, hs *Handshake, addr net.Addr) {
	loginData := trailing(initial, hs.End)
	if len(loginData) == 0 {
		conn.SetReadDeadline(time.Now().Add(loginReadTimeout))
		buf := make([]byte, readBufferSize)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			log.Errorf("Transfer failed for %s: no login packet", addr)
			return
		}
		loginData = buf[:n]
	}

	name, uuid, err := parseLoginStart(loginData)
	if err != nil {
		log.Warnf("Failed to parse login start from %s: %v", addr, err)
		return
	}

	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	if _, err := conn.Write(makeLoginSuccess(uuid, name)); err != nil {
		log.Errorf("Transfer failed for %s: %v", addr, err)
		return
	}

	// Wait for the login acknowledged packet that moves the client into the
	// configuration state, where the transfer packet is valid.
	conn.SetReadDeadline(time.Now().Add(statusTimeout))
	ack := make([]byte, readBufferSize)
	if _, err := conn.Read(ack); err != nil {
		log.Errorf("Transfer failed for %s: %v", addr, err)
		return
	}

	targetHost, targetPort := h.cfg.Transfer.Host, h.cfg.Transfer.Port
	if h.isLocalClient(addr) {
		targetHost, targetPort = h.cfg.Server.IP, h.cfg.Server.MCPort
	}

	log.Infof("Transferring %s to %s:%d", sanitizeForLog(name, 64), targetHost, targetPort)
	conn.SetWriteDeadline(time.Now().Add(statusTimeout))
	conn.Write(makeTransferPacket(targetHost, targetPort))
}

func (h *Handler) proxy(ctx context.Context, client net.Conn, initial []byte) {
	target := net.JoinHostPort(h.cfg.Server.IP, strconv.Itoa(h.cfg.Server.MCPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	server, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Errorf("Failed to connect to MC server for %s: %v", client.RemoteAddr(), err)
		return
	}
	defer server.Close()

	log.Infof("Forwarding connection from %s to %s", client.RemoteAddr(), target)

	// The deadlines from the handshake must not cut the session short.
	client.SetDeadline(time.Time{})
	if _, err := server.Write(initial); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go pipe(&wg, server, client)
	go pipe(&wg, client, server)
	wg.Wait()

	log.Infof("Connection from %s closed", client.RemoteAddr())
}

// Half closes the destination when the source is done, so the peer notices.
func pipe(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	io.Copy(dst, src)
	if closer, ok := dst.(interface{ CloseWrite() error }); ok {
		closer.CloseWrite()
		return
	}
	dst.Close()
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

func trailing(data []byte, offset int) []byte {
	if offset < 0 || offset >= len(data) {
		return nil
	}
	return data[offset:]
}
