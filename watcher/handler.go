package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	readBufferSize   = 4096
	handshakeTimeout = 10 * time.Second
	statusTimeout    = 5 * time.Second
	loginReadTimeout = 10 * time.Second
	// A status response with a 64x64 icon is around 10 kB, the rest is headroom
	// for large player samples.
	maxStatusResponseBytes = 256 * 1024
)

type Handler struct {
	cfg       *Config
	waker     *Waker
	assets    *Assets
	localNets []*net.IPNet

	statusConnections *ConnectionLimiter
	loginConnections  *ConnectionLimiter
}

func NewHandler(cfg *Config, waker *Waker) *Handler {
	h := &Handler{
		cfg:               cfg,
		waker:             waker,
		assets:            NewAssets(cfg),
		localNets:         cfg.ParsedLocalNetworks(),
		statusConnections: NewConnectionLimiter("status", minStatusConnections, cfg.Limits.MaxPerIP),
		loginConnections:  NewConnectionLimiter("login", cfg.MOTD.MaxPlayers, cfg.Limits.MaxPerIP),
	}
	h.refreshConnectionLimits()
	return h
}

// limits.max_logins wins when it is set, otherwise the player slots the server
// reported are the natural cap, since more players cannot join anyway. Before
// the server has ever been reached, motd.max_players stands in.
func (h *Handler) refreshConnectionLimits() {
	logins := h.cfg.Limits.MaxLogins
	if logins <= 0 {
		logins = h.cfg.MOTD.MaxPlayers
		if info := h.waker.CachedInfo(); info != nil && info.MaxPlayers > 0 {
			logins = info.MaxPlayers
		}
	}
	if logins < 1 {
		logins = 1
	}
	h.loginConnections.SetMax(logins)
	h.statusConnections.SetMax(max(logins*statusPerLogin, minStatusConnections))
}

func (h *Handler) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr()

	h.refreshConnectionLimits()
	if !h.statusConnections.Acquire(addr) {
		return
	}
	statusSlotHeld := true
	releaseStatusSlot := func() {
		if statusSlotHeld {
			statusSlotHeld = false
			h.statusConnections.Release(addr)
		}
	}
	defer releaseStatusSlot()

	initial, handshake := h.readHandshake(conn)
	if handshake == nil {
		return
	}

	if !h.isAllowedHostname(handshake, addr) {
		return
	}

	switch handshake.NextState {
	case 1:
		h.handleStatus(ctx, conn, initial, handshake)
	case 2:
		h.handleLogin(ctx, conn, initial, handshake, addr, releaseStatusSlot)
	}
}

// TCP may split the handshake, so it is accumulated until it parses.
// Returns a nil handshake when nothing usable arrived.
func (h *Handler) readHandshake(conn net.Conn) ([]byte, *Handshake) {
	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))

	buf := make([]byte, 0, readBufferSize)
	chunk := make([]byte, readBufferSize)
	for len(buf) < readBufferSize {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			handshake, parseErr := parseHandshake(buf)
			if parseErr == nil {
				return buf, handshake
			}
			// Anything other than a short read is a packet that will not
			// become valid by waiting for more of it.
			if !errors.Is(parseErr, ErrIncompleteVarInt) && !errors.Is(parseErr, ErrShortPacket) {
				return nil, nil
			}
		}
		if err != nil {
			return nil, nil
		}
	}
	return nil, nil
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

	// Clients can pack handshake, status request, and ping in one segment; read only when nothing followed.
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

	response, err := makeStatusResponse(motd, maxPlayers, 0, h.assets.Icon(), versionName, versionProtocol)
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

func (h *Handler) handleLogin(ctx context.Context, conn net.Conn, initial []byte, hs *Handshake, addr net.Addr, releaseStatusSlot func()) {
	log.Infof("Login attempt from %s", addr)

	// A login holds its slot for the whole session, so it moves out of the short
	// lived status pool into the one sized after the player slots.
	if !h.loginConnections.Acquire(addr) {
		conn.SetWriteDeadline(time.Now().Add(statusTimeout))
		conn.Write(makeLoginDisconnect(h.cfg.MOTD.ServerFull))
		return
	}
	defer h.loginConnections.Release(addr)
	releaseStatusSlot()

	// The sleep monitor reads this instead of polling the server, which is what
	// keeps it from fighting the container's own autopause.
	h.waker.SessionStarted()
	defer h.waker.SessionEnded()

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

// Client receives a transfer packet, traffic skips the watcher.
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
	if _, err := conn.Write(makeLoginSuccess(uuid, name, hs.ProtocolVersion)); err != nil {
		log.Errorf("Transfer failed for %s: %v", addr, err)
		return
	}

	// Wait for login acknowledged packet: client moves to configuration state, where transfer is valid.
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

// Port scanners connect by raw IP, so only listed names get an answer at all.
func (h *Handler) isAllowedHostname(hs *Handshake, addr net.Addr) bool {
	if len(h.cfg.Watcher.AllowedHostnames) == 0 {
		return true
	}
	if h.isLocalClient(addr) {
		return true
	}
	requested := normalizeServerAddress(hs.ServerAddress)
	for _, allowed := range h.cfg.Watcher.AllowedHostnames {
		if strings.EqualFold(normalizeServerAddress(allowed), requested) {
			return true
		}
	}
	log.Infof("Dropping connection from %s: hostname %q not in allowed list", addr, sanitizeForLog(requested, 100))
	return false
}

// Forge appends a NUL and "FML3" to the address, proxies that forward the
// player IP append a NUL and their own fields, so only the part before the
// first NUL is the hostname the player typed. A trailing dot is the DNS root
// and names the same host, some clients keep it.
func normalizeServerAddress(address string) string {
	if end := strings.IndexByte(address, 0); end >= 0 {
		address = address[:end]
	}
	return strings.TrimSuffix(strings.TrimSpace(address), ".")
}

func trailing(data []byte, offset int) []byte {
	if offset < 0 || offset >= len(data) {
		return nil
	}
	return data[offset:]
}
