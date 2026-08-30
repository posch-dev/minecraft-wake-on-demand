package proxy

import (
	"net"
	"sync"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// Separate pools: shared, server list pings would eat the slots players need.
const (
	minStatusConnections = 64
	statusPerLogin       = 5
	limitLogInterval     = time.Minute
)

// Caps how many connections of one kind are open at once, in total and per
// source IP, so a flood cannot exhaust memory and file descriptors on a Pi.
type ConnectionLimiter struct {
	kind string

	mu        sync.Mutex
	max       int
	perIP     int
	inUse     int
	byIP      map[string]int
	lastLogAt time.Time
}

func NewConnectionLimiter(kind string, max, perIP int) *ConnectionLimiter {
	return &ConnectionLimiter{kind: kind, max: max, perIP: perIP, byIP: map[string]int{}}
}

func (l *ConnectionLimiter) SetMax(max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.max = max
}

// Reports whether a slot was taken. Every successful call needs one Release.
func (l *ConnectionLimiter) Acquire(addr net.Addr) bool {
	ip := hostOf(addr)

	l.mu.Lock()
	defer l.mu.Unlock()

	reason := ""
	switch {
	case l.max > 0 && l.inUse >= l.max:
		reason = "limit reached"
	case l.perIP > 0 && l.byIP[ip] >= l.perIP:
		reason = "too many connections from this address"
	}
	if reason != "" {
		// Logging every rejected connection would turn a flood into a second
		// flood in the journal.
		if time.Since(l.lastLogAt) >= limitLogInterval {
			l.lastLogAt = time.Now()
			logging.Warnf("Refusing %s connection from %s: %s (%d of %d in use)",
				l.kind, ip, reason, l.inUse, l.max)
		}
		return false
	}

	l.inUse++
	l.byIP[ip]++
	return true
}

func (l *ConnectionLimiter) Release(addr net.Addr) {
	ip := hostOf(addr)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.inUse > 0 {
		l.inUse--
	}
	if l.byIP[ip] <= 1 {
		delete(l.byIP, ip)
		return
	}
	l.byIP[ip]--
}

func (l *ConnectionLimiter) InUse() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inUse
}

func hostOf(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
