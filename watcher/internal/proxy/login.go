package proxy

import (
	"context"
	"net"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

func (h *Handler) handleLogin(ctx context.Context, conn net.Conn, initial []byte, hs *mcproto.Handshake, addr net.Addr, releaseStatusSlot func()) {
	logging.Infof("Login attempt from %s", addr)

	// A login holds its slot for the whole session, so it moves out of the short
	// lived status pool into the one sized after the player slots.
	if !h.loginConnections.Acquire(addr) {
		conn.SetWriteDeadline(time.Now().Add(statusTimeout))
		conn.Write(mcproto.MakeLoginDisconnect(h.cfg.MOTD.ServerFull))
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
		logging.Infof("Login from %s starts the server, sending the wait message", addr)
		conn.SetWriteDeadline(time.Now().Add(statusTimeout))
		conn.Write(mcproto.MakeLoginDisconnect(h.assets.MOTDLoginWait()))
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

// The boot outlives the connection that asked for it, so the server is up by
// the time that player comes back.
func (h *Handler) bootInBackground(ctx context.Context, addr net.Addr) {
	if h.waker.FullBoot(ctx) {
		logging.Infof("Server is ready, %s can reconnect", addr)
		return
	}
	logging.Errorf("The boot %s asked for did not finish", addr)
}

// Closing while the login packet is still unread resets the connection, and the
// client reports a socket error instead of showing the message it was just
// sent. Waiting a moment for it costs nothing, the answer is already out.
func drainBeforeClose(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(loginDrainTimeout))
	conn.Read(make([]byte, readBufferSize))
}
