package proxy

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/assets"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/boot"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

const (
	readBufferSize    = 4096
	handshakeTimeout  = 10 * time.Second
	statusTimeout     = 5 * time.Second
	loginReadTimeout  = 10 * time.Second
	loginDrainTimeout = 1 * time.Second
	// A status response with a 64x64 icon is around 10 kB, the rest is headroom
	// for large player samples.
)

type Handler struct {
	cfg       *config.Config
	waker     *boot.Waker
	assets    *assets.Assets
	localNets []*net.IPNet

	statusConnections *ConnectionLimiter
	loginConnections  *ConnectionLimiter
}

func NewHandler(cfg *config.Config, waker *boot.Waker) *Handler {
	h := &Handler{
		cfg:               cfg,
		waker:             waker,
		assets:            assets.NewAssets(cfg),
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
func (h *Handler) readHandshake(conn net.Conn) ([]byte, *mcproto.Handshake) {
	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))

	buf := make([]byte, 0, readBufferSize)
	chunk := make([]byte, readBufferSize)
	for len(buf) < readBufferSize {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			handshake, parseErr := mcproto.ParseHandshake(buf)
			if parseErr == nil {
				return buf, handshake
			}
			// Anything other than a short read is a packet that will not
			// become valid by waiting for more of it.
			if !errors.Is(parseErr, mcproto.ErrIncompleteVarInt) && !errors.Is(parseErr, mcproto.ErrShortPacket) {
				return nil, nil
			}
		}
		if err != nil {
			return nil, nil
		}
	}
	return nil, nil
}

func trailing(data []byte, offset int) []byte {
	if offset < 0 || offset >= len(data) {
		return nil
	}
	return data[offset:]
}

// The assets refresh runs for as long as the watcher does, so whoever owns the
// process starts it.
func (h *Handler) KeepAssetsFresh(ctx context.Context) { h.assets.KeepFresh(ctx) }
