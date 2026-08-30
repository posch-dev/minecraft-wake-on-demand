package proxy

import (
	"net"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// Port scanners connect by raw IP, so only listed names get an answer at all.
func (h *Handler) isAllowedHostname(hs *mcproto.Handshake, addr net.Addr) bool {
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
	logging.Infof("Dropping connection from %s: hostname %q not in allowed list", addr, logging.Sanitize(requested, 100))
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
