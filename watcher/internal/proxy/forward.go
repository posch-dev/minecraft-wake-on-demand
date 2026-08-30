package proxy

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func (h *Handler) proxy(ctx context.Context, client net.Conn, initial []byte) {
	target := net.JoinHostPort(h.cfg.Server.IP, strconv.Itoa(h.cfg.Server.MCPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	server, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		logging.Errorf("Failed to connect to MC server for %s: %v", client.RemoteAddr(), err)
		return
	}
	defer server.Close()

	logging.Infof("Forwarding connection from %s to %s", client.RemoteAddr(), target)

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

	logging.Infof("Connection from %s closed", client.RemoteAddr())
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
