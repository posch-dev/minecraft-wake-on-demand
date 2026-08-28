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
	cfg    *Config
	pinger *Pinger
	ssh    *SSHRunner

	// Always 9 in production, the tests point it at a local listener.
	wolPort int

	// Serializes whole boot sequences, only one wake runs at a time.
	bootMu sync.Mutex

	stateMu     sync.Mutex
	booting     bool
	lastAttempt time.Time
	failures    int

	reachMu      sync.Mutex
	reachValue   bool
	reachChecked time.Time

	infoMu sync.Mutex
	info   *ServerInfo
}

func NewWaker(cfg *Config) *Waker {
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
	path := w.cfg.ServerInfoPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var info ServerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		log.Warnf("Ignoring corrupt server info cache %s: %v", path, err)
		return
	}
	w.info = &info
	log.Infof("Loaded cached server info: %s (protocol %d, max players %d)",
		info.Name, info.Protocol, info.MaxPlayers)
}

// The cache holds nothing secret, 0600 only keeps other accounts on the watcher
// from feeding the proxy a version it never probed.
func (w *Waker) saveServerInfo(info *ServerInfo) {
	path := w.cfg.ServerInfoPath()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		log.Warnf("Cannot encode server info cache: %v", err)
		return
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		log.Warnf("Cannot write server info cache %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Warnf("Cannot update server info cache %s: %v", path, err)
		os.Remove(tmpPath)
	}
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
	parsed, err := ParseMAC(mac)
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
	log.Infof("WoL magic packet sent to %s (%s mode)", target, w.cfg.WoL.Mode)
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
	value := dialSucceeds(ctx, w.mcAddress(), 2*time.Second)
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
	log.Infof("Learned server info: %s (protocol %d, max players %d)",
		learned.Name, learned.Protocol, learned.MaxPlayers)
	go w.saveServerInfo(learned)
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
		return
	}
	w.failures++
	log.Warnf("Boot sequence failed (%d in a row), next attempt in %ds",
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
	log.Errorf("%s did not become ready within %ds", label, timeout)
	return false
}

func (w *Waker) waitForHost(ctx context.Context) bool {
	log.Infof("Waiting for server PC to respond to ping (timeout %ds)...", w.cfg.Timeouts.BootTimeout)
	ok := w.waitFor(ctx, "Server PC", w.cfg.Timeouts.BootTimeout, func(ctx context.Context) bool {
		return w.pinger.Ping(ctx, w.cfg.Server.IP, 1500*time.Millisecond)
	})
	if ok {
		log.Infof("Server PC is up")
	}
	return ok
}

// SSH is rarely up the moment the machine answers a ping.
func (w *Waker) waitForSSH(ctx context.Context) bool {
	return w.waitFor(ctx, "SSH port", 30, w.SSHPortReachable)
}

func (w *Waker) waitForMC(ctx context.Context) bool {
	log.Infof("Waiting for Minecraft port %d (timeout %ds)...",
		w.cfg.Server.MCPort, w.cfg.Timeouts.MCReadyTimeout)
	ok := w.waitFor(ctx, "Minecraft server", w.cfg.Timeouts.MCReadyTimeout, w.mcAcceptsStatus)
	if ok {
		log.Infof("Minecraft server is ready")
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
		log.Warnf("Boot attempt refused, %ds cooldown remaining (%d consecutive failures)",
			int(cooldown.Seconds()), failures)
		return false
	}

	w.beginBoot()
	ok := false
	defer func() { w.endBoot(ok) }()

	if w.SSHPortReachable(ctx) {
		log.Infof("Server PC is up but MC not running, starting container...")
	} else {
		log.Infof("Server PC is sleeping, sending WoL...")
		if err := w.SendMagicPacket(); err != nil {
			log.Errorf("WoL failed: %v", err)
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
		log.Errorf("Container start failed: %v", err)
		return false
	}

	ok = w.waitForMC(ctx)
	if ok {
		w.markReachable()
	}
	return ok
}
