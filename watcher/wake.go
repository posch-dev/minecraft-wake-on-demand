package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// A burst of connections shares one probe instead of hammering the server.
const mcReachableTTL = 2 * time.Second

// The discard protocol port, where wake capable hardware listens.
const wolPort = 9

// What a status probe told us about the running server, kept across restarts so
// the watcher can answer for it while it sleeps.
type ServerInfo struct {
	Name       string    `json:"name"`
	Protocol   int       `json:"protocol"`
	MaxPlayers int       `json:"max_players"`
	Updated    time.Time `json:"updated"`
}

type Waker struct {
	cfg    *config.Config
	pinger *Pinger
	ssh    *SSHRunner

	// Always 9 in production, the tests point it at a local listener.
	wolPort int

	// Serializes whole boot sequences, only one wake runs at a time.
	bootMu sync.Mutex

	stateMu     sync.Mutex
	booting     bool
	lastAttempt time.Time
	lastBootAt  time.Time

	// Players forwarded through the watcher right now. Free to keep and exact
	// in proxy mode, which is what saves the sleep monitor from polling.
	sessionMu      sync.Mutex
	activeSessions int
	lastSessionEnd time.Time
	failures       int

	reachMu      sync.Mutex
	reachValue   bool
	reachChecked time.Time

	infoMu sync.Mutex
	info   *ServerInfo
}

func NewWaker(cfg *config.Config) *Waker {
	w := &Waker{
		cfg:     cfg,
		wolPort: wolPort,
		pinger:  &Pinger{},
		ssh:     NewSSHRunner(cfg),
	}
	w.loadServerInfo()
	return w
}

func (w *Waker) loadServerInfo() {
	cache := readServerInfoCache(w.cfg.ServerInfoPath())
	info, ok := cache[w.cfg.ServerInfoKey()]
	if !ok {
		return
	}
	w.info = info
	logging.Infof("Loaded cached server info: %s (protocol %d, max players %d)",
		info.Name, info.Protocol, info.MaxPlayers)
}

func (w *Waker) saveServerInfo(info *ServerInfo) {
	path := w.cfg.ServerInfoPath()
	cache := readServerInfoCache(path)
	cache[w.cfg.ServerInfoKey()] = info
	writeServerInfoCache(path, cache)
}

// Empty rather than nil on any problem, so a caller can always write into it.
// An older single world file does not fit the shape and is left to be replaced.
func readServerInfoCache(path string) map[string]*ServerInfo {
	cache := map[string]*ServerInfo{}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		logging.Infof("Replacing the server info cache %s, it is from an older version", path)
		return map[string]*ServerInfo{}
	}
	return cache
}

// The cache holds nothing secret, 0600 only keeps other accounts on the watcher
// from feeding the proxy a version it never probed.
func writeServerInfoCache(path string, cache map[string]*ServerInfo) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		logging.Warnf("Cannot encode server info cache: %v", err)
		return
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		logging.Warnf("Cannot write server info cache %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		logging.Warnf("Cannot update server info cache %s: %v", path, err)
		os.Remove(tmpPath)
	}
}

// A world that changed its Minecraft version would otherwise keep claiming the
// old one until someone connects.
func forgetServerInfo(cfg *config.Config, world string) {
	path := cfg.ServerInfoPath()
	cache := readServerInfoCache(path)
	if _, ok := cache[world]; !ok {
		return
	}
	delete(cache, world)
	writeServerInfoCache(path, cache)
}

func (w *Waker) CachedInfo() *ServerInfo {
	w.infoMu.Lock()
	defer w.infoMu.Unlock()
	return w.info
}

func (w *Waker) mcAddress() string {
	return net.JoinHostPort(w.cfg.Server.IP, strconv.Itoa(w.cfg.Server.MCPort))
}

func (w *Waker) Booting() bool {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.booting
}

// Six 0xFF bytes followed by the MAC sixteen times, 102 bytes in total.
func buildMagicPacket(mac string) ([]byte, error) {
	parsed, err := config.ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	payload := bytes.Repeat([]byte{0xFF}, 6)
	for i := 0; i < 16; i++ {
		payload = append(payload, parsed...)
	}
	return payload, nil
}

func (w *Waker) SendMagicPacket() error {
	payload, err := buildMagicPacket(w.cfg.Server.MAC)
	if err != nil {
		return err
	}

	target := w.cfg.Server.IP
	broadcast := w.cfg.WoL.Mode == "broadcast"
	if broadcast {
		target = w.cfg.WoL.BroadcastAddress
	}

	dialer := net.Dialer{Timeout: 5 * time.Second}
	if broadcast {
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) { sockErr = setBroadcast(fd) }); err != nil {
				return err
			}
			return sockErr
		}
	}

	conn, err := dialer.Dial("udp", net.JoinHostPort(target, strconv.Itoa(w.wolPort)))
	if err != nil {
		return fmt.Errorf("cannot open WoL socket: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("cannot send WoL packet: %w", err)
	}
	logging.Infof("WoL magic packet sent to %s (%s mode)", target, w.cfg.WoL.Mode)
	return nil
}

// force skips the cache, needed once the boot lock is held because another
// goroutine may have finished a boot in the meantime.
func (w *Waker) MCPortReachable(ctx context.Context, force bool) bool {
	w.reachMu.Lock()
	defer w.reachMu.Unlock()

	if !force && time.Since(w.reachChecked) < mcReachableTTL {
		return w.reachValue
	}
	value := w.mcAcceptsStatus(ctx)
	w.reachValue = value
	w.reachChecked = time.Now()
	return value
}

func (w *Waker) markReachable() {
	w.reachMu.Lock()
	defer w.reachMu.Unlock()
	w.reachValue = true
	w.reachChecked = time.Now()
}

func (w *Waker) SSHPortReachable(ctx context.Context) bool {
	return dialSucceeds(ctx, w.ssh.Address(), 2*time.Second)
}

func dialSucceeds(ctx context.Context, address string, timeout time.Duration) bool {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// An open port is not enough, the container may still be booting.
func (w *Waker) mcAcceptsStatus(ctx context.Context) bool {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", w.mcAddress())
	if err != nil {
		return false
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	request := append(
		makeStatusHandshake(w.cfg.Server.IP, w.cfg.Server.MCPort),
		makeStatusRequest()...,
	)
	if _, err := conn.Write(request); err != nil {
		return false
	}
	body, err := readFramedPacket(conn, maxStatusResponseBytes)
	if err != nil {
		return false
	}
	w.learnServerInfo(body)
	return true
}

// Takes version and player slots from the body of a status response and caches
// them when anything changed.
func (w *Waker) learnServerInfo(body []byte) {
	payload, err := parseStatusPayload(body)
	if err != nil {
		return
	}
	if payload.Version.Name == "" || payload.Version.Protocol == 0 {
		return
	}

	w.infoMu.Lock()
	defer w.infoMu.Unlock()

	learned := &ServerInfo{
		Name:       payload.Version.Name,
		Protocol:   payload.Version.Protocol,
		MaxPlayers: payload.Players.Max,
		Updated:    time.Now(),
	}
	if w.info != nil && w.info.Name == learned.Name &&
		w.info.Protocol == learned.Protocol && w.info.MaxPlayers == learned.MaxPlayers {
		return
	}

	w.info = learned
	logging.Infof("Learned server info: %s (protocol %d, max players %d)",
		learned.Name, learned.Protocol, learned.MaxPlayers)
	go w.saveServerInfo(learned)
}

// When the server PC last finished booting, so the sleep monitor can leave it
// alone long enough for the first player to actually get in.
func (w *Waker) LastBootAt() time.Time {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.lastBootAt
}

func (w *Waker) SessionStarted() {
	w.sessionMu.Lock()
	defer w.sessionMu.Unlock()
	w.activeSessions++
}

func (w *Waker) SessionEnded() {
	w.sessionMu.Lock()
	defer w.sessionMu.Unlock()
	if w.activeSessions > 0 {
		w.activeSessions--
	}
	if w.activeSessions == 0 {
		w.lastSessionEnd = time.Now()
	}
}

// Number of forwarded sessions, and when the last one ended. A zero time with
// no sessions means none has ever been forwarded since the watcher started.
func (w *Waker) Sessions() (int, time.Time) {
	w.sessionMu.Lock()
	defer w.sessionMu.Unlock()
	return w.activeSessions, w.lastSessionEnd
}

// Ping only. The PC can be awake with the container stopped, which is exactly a
// case worth sleeping, so this must not depend on the Minecraft port.
func (w *Waker) HostReachable(ctx context.Context) bool {
	return w.pinger.Ping(ctx, w.cfg.Server.IP, 1500*time.Millisecond)
}

func (w *Waker) cooldownRemaining() time.Duration {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.cooldownRemainingLocked()
}

func (w *Waker) cooldownRemainingLocked() time.Duration {
	if w.lastAttempt.IsZero() {
		return 0
	}
	delay := time.Duration(w.cfg.Limits.BootCooldown) * time.Second
	if w.failures > 0 {
		maxBackoff := time.Duration(w.cfg.Limits.BootMaxBackoff) * time.Second
		delay = maxBackoff
		// Guard the shift, the failure counter has no upper bound.
		if w.failures <= 30 {
			scaled := time.Duration(w.cfg.Limits.BootFailureBackoff) * time.Second << uint(w.failures-1)
			if scaled > 0 && scaled < maxBackoff {
				delay = scaled
			}
		}
	}
	remaining := delay - time.Since(w.lastAttempt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (w *Waker) beginBoot() {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.lastAttempt = time.Now()
	w.booting = true
}

func (w *Waker) endBoot(ok bool) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.booting = false
	if ok {
		w.failures = 0
		w.lastBootAt = time.Now()
		return
	}
	w.failures++
	logging.Warnf("Boot sequence failed (%d in a row), next attempt in %ds",
		w.failures, int(w.cooldownRemainingLocked().Seconds()))
}

func (w *Waker) waitFor(ctx context.Context, label string, timeout int, probe func(context.Context) bool) bool {
	attempts := timeout / 2
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if probe(ctx) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
	logging.Errorf("%s did not become ready within %ds", label, timeout)
	return false
}

func (w *Waker) waitForHost(ctx context.Context) bool {
	logging.Infof("Waiting for server PC to respond to ping (timeout %ds)...", w.cfg.Timeouts.BootTimeout)
	ok := w.waitFor(ctx, "Server PC", w.cfg.Timeouts.BootTimeout, func(ctx context.Context) bool {
		return w.pinger.Ping(ctx, w.cfg.Server.IP, 1500*time.Millisecond)
	})
	if ok {
		logging.Infof("Server PC is up")
	}
	return ok
}

// SSH is rarely up the moment the machine answers a ping.
func (w *Waker) waitForSSH(ctx context.Context) bool {
	return w.waitFor(ctx, "SSH port", 30, w.SSHPortReachable)
}

func (w *Waker) waitForMC(ctx context.Context) bool {
	logging.Infof("Waiting for Minecraft port %d (timeout %ds)...",
		w.cfg.Server.MCPort, w.cfg.Timeouts.MCReadyTimeout)
	ok := w.waitFor(ctx, "Minecraft server", w.cfg.Timeouts.MCReadyTimeout, w.mcAcceptsStatus)
	if ok {
		logging.Infof("Minecraft server is ready")
	}
	return ok
}

func (w *Waker) FullBoot(ctx context.Context) bool {
	w.bootMu.Lock()
	defer w.bootMu.Unlock()

	// Another goroutine may have finished a boot while we waited for the lock.
	if w.MCPortReachable(ctx, true) {
		return true
	}
	if cooldown := w.cooldownRemaining(); cooldown > 0 {
		w.stateMu.Lock()
		failures := w.failures
		w.stateMu.Unlock()
		logging.Warnf("Boot attempt refused, %ds cooldown remaining (%d consecutive failures)",
			int(cooldown.Seconds()), failures)
		return false
	}

	w.beginBoot()
	ok := false
	defer func() { w.endBoot(ok) }()

	if w.SSHPortReachable(ctx) {
		logging.Infof("Server PC is up but MC not running, starting container...")
	} else {
		logging.Infof("Server PC is sleeping, sending WoL...")
		if err := w.SendMagicPacket(); err != nil {
			logging.Errorf("WoL failed: %v", err)
			return false
		}
		if !w.waitForHost(ctx) {
			return false
		}
		if !w.waitForSSH(ctx) {
			return false
		}
	}

	if err := w.ssh.StartContainer(ctx); err != nil {
		logging.Errorf("Container start failed: %v", err)
		return false
	}

	ok = w.waitForMC(ctx)
	if ok {
		w.markReachable()
	}
	return ok
}
