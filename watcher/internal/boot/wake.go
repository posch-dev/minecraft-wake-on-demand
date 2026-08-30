package boot

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/netprobe"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/serverinfo"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/wol"
)

// A burst of connections shares one probe instead of hammering the server.
const mcReachableTTL = 2 * time.Second

// The discard protocol port, where wake capable hardware listens.

type Waker struct {
	cfg    *config.Config
	pinger *netprobe.Pinger
	ssh    *sshx.SSHRunner

	// Always 9 in production, the tests point it at a local listener.

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
	info   *serverinfo.Info
}

func NewWaker(cfg *config.Config) *Waker {
	w := &Waker{
		cfg:    cfg,
		pinger: &netprobe.Pinger{},
		ssh:    sshx.NewSSHRunner(cfg),
	}
	w.info = serverinfo.Load(w.cfg)
	return w
}

func (w *Waker) CachedInfo() *serverinfo.Info {
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

func (w *Waker) MarkReachable() {
	w.reachMu.Lock()
	defer w.reachMu.Unlock()
	w.reachValue = true
	w.reachChecked = time.Now()
}

func (w *Waker) SSHPortReachable(ctx context.Context) bool {
	return DialSucceeds(ctx, w.ssh.Address(), 2*time.Second)
}

func DialSucceeds(ctx context.Context, address string, timeout time.Duration) bool {
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
		mcproto.MakeStatusHandshake(w.cfg.Server.IP, w.cfg.Server.MCPort),
		mcproto.MakeStatusRequest()...,
	)
	if _, err := conn.Write(request); err != nil {
		return false
	}
	body, err := mcproto.ReadFramedPacket(conn, mcproto.MaxStatusResponseBytes)
	if err != nil {
		return false
	}
	w.learnServerInfo(body)
	return true
}

// Takes version and player slots from the body of a status response and caches
// them when anything changed.
func (w *Waker) learnServerInfo(body []byte) {
	payload, err := mcproto.ParseStatusPayload(body)
	if err != nil {
		return
	}
	if payload.Version.Name == "" || payload.Version.Protocol == 0 {
		return
	}

	w.infoMu.Lock()
	defer w.infoMu.Unlock()

	learned := &serverinfo.Info{
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
	go serverinfo.Save(w.cfg, learned)
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
		if err := wol.Send(w.cfg, wol.Port); err != nil {
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

	if err := remote.StartContainer(ctx, w.ssh); err != nil {
		logging.Errorf("Container start failed: %v", err)
		return false
	}

	ok = w.waitForMC(ctx)
	if ok {
		w.MarkReachable()
	}
	return ok
}

// A test can hand the waker what a probe would have learned.
func (w *Waker) SetInfo(info *serverinfo.Info) {
	w.infoMu.Lock()
	defer w.infoMu.Unlock()
	w.info = info
}

// A test drives the cooldown by hand rather than waiting it out.
func (w *Waker) SetLastAttempt(t time.Time) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.lastAttempt = t
}

// A test moves the last boot into the past instead of waiting for the grace
// period to run out.
func (w *Waker) SetLastBoot(t time.Time) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.lastBootAt = t
}

// The sleep tests need an idle server without playing a session out.
func (w *Waker) SetLastSessionEnd(t time.Time) {
	w.sessionMu.Lock()
	defer w.sessionMu.Unlock()
	w.lastSessionEnd = t
}
