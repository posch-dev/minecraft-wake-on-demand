package main

import (
	"bytes"
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
	readBufferSize    = 4096
	handshakeTimeout  = 10 * time.Second
	statusTimeout     = 5 * time.Second
	loginReadTimeout  = 10 * time.Second
	loginDrainTimeout = 1 * time.Second
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

// The server's own player slots are the natural cap, more cannot join anyway.
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

// A live server answers for itself. If it does not, the watcher answers rather
// than leaving the entry in the server list looking broken.
func (h *Handler) handleStatus(ctx context.Context, conn net.Conn, initial []byte, hs *Handshake) {
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

func (h *Handler) answerStatus(ctx context.Context, conn net.Conn, rest []byte, hs *Handshake) {
	motd, icon := h.assets.MOTDSleeping(), h.assets.IconSleeping()
	if h.waker.Booting() {
		motd, icon = h.assets.MOTDStarting(), h.assets.IconStarting()
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

	response, err := makeStatusResponse(motd, maxPlayers, 0, icon, versionName, versionProtocol)
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

	// Booting takes half a minute, longer than a client waits, so the player is
	// told to come back rather than left staring at a bar that then fails.
	if !h.waker.MCPortReachable(ctx, false) {
		log.Infof("Login from %s starts the server, sending the wait message", addr)
		conn.SetWriteDeadline(time.Now().Add(statusTimeout))
		conn.Write(makeLoginDisconnect(h.assets.MOTDLoginWait()))
		drainBeforeClose(conn)
		go h.bootInBackground(ctx, addr)
		return
	}

	if h.cfg.Transfer.Enabled {
		h.handleTransfer(ctx, conn, initial, hs, addr)
		return
	}
	h.proxy(ctx, conn, initial)
}

// Forwards the ping to the real server, then swaps in the watcher's own MOTD
// and icon if either is overridden. Player count and version stay real.
// False means nothing reached the client, so the caller can still answer.
func (h *Handler) proxyStatus(ctx context.Context, conn net.Conn, initial, rest []byte) bool {
	target := net.JoinHostPort(h.cfg.Server.IP, strconv.Itoa(h.cfg.Server.MCPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	server, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Warnf("Cannot reach %s for a status ping: %v", target, err)
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
	body, err := readFramedPacket(server, maxStatusResponseBytes)
	if err != nil {
		log.Warnf("Cannot read the status response from %s: %v", target, err)
		return false
	}

	response, err := h.dressStatusResponse(body)
	if err != nil {
		log.Warnf("Cannot rebuild the status response from %s: %v", target, err)
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
		return framePacket(body), nil
	}
	return rewriteStatusResponse(body, motd, icon)
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

// Closing while the login packet is still unread resets the connection, and the
// client reports a socket error instead of showing the message it was just
// sent. Waiting a moment for it costs nothing, the answer is already out.
func drainBeforeClose(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(loginDrainTimeout))
	conn.Read(make([]byte, readBufferSize))
}

// The boot outlives the connection that asked for it, so the server is up by
// the time that player comes back.
func (h *Handler) bootInBackground(ctx context.Context, addr net.Addr) {
	if h.waker.FullBoot(ctx) {
		log.Infof("Server is ready, %s can reconnect", addr)
		return
	}
	log.Errorf("The boot %s asked for did not finish", addr)
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

// Forge and forwarding proxies append their own fields after a NUL byte.
// A trailing dot is the DNS root and names the same host.
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
