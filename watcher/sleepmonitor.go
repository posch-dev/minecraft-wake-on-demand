package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// A client that connects and goes quiet would hold the counter above zero forever.
const maxCounterTrust = time.Hour

// Proxy mode reads the counter from memory, transfer mode pays an SSH round trip.
const proxyTickInterval = 30 * time.Second

type SleepMonitor struct {
	cfg   *Config
	waker *Waker

	// Fields, not methods, so tests can drive the machine without a server.
	now           func() time.Time
	hostReachable func(ctx context.Context) bool
	runVerb       func(ctx context.Context, verb string) (string, error)
	stopContainer func(ctx context.Context) error

	pendingSince    time.Time
	lastPlayerQuery time.Time
	zeroPlayersFrom time.Time
	lastReason      string
	// Sticky, so a stuck counter cannot cancel every confirmation it triggered.
	counterDistrusted bool
}

func NewSleepMonitor(cfg *Config, waker *Waker) *SleepMonitor {
	runner := NewSSHRunner(cfg)
	return &SleepMonitor{
		cfg:             cfg,
		waker:           waker,
		now:             time.Now,
		lastPlayerQuery: time.Now(),
		hostReachable:   waker.HostReachable,
		runVerb:         runner.RunVerb,
		stopContainer:   runner.StopContainer,
	}
}

func runSleepMonitor(ctx context.Context, cfg *Config, waker *Waker) {
	monitor := NewSleepMonitor(cfg, waker)
	logging.Infof("Sleep monitor active, %s after %ds without players",
		cfg.Sleep.Action, cfg.Sleep.IdleAfter)

	ticker := time.NewTicker(monitor.tickInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			monitor.tick(ctx)
		}
	}
}

func (m *SleepMonitor) tickInterval() time.Duration {
	if m.cfg.Transfer.Enabled {
		return seconds(m.cfg.Sleep.PollInterval)
	}
	return proxyTickInterval
}

func (m *SleepMonitor) tick(ctx context.Context) {
	if m.waker.Booting() {
		m.hold("the server is still booting")
		return
	}
	if m.within(m.waker.LastBootAt(), m.cfg.Sleep.GracePeriod) {
		m.hold("still inside the grace period after waking")
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if !m.hostReachable(probeCtx) {
		m.hold("the server PC does not answer, it is already asleep")
		return
	}
	if idle, reason := m.looksIdle(); !idle {
		m.hold(reason)
		return
	}

	// Not knowing has to count as busy, or the machine suspends under someone.
	online, ok := m.playersOnline(probeCtx)
	if !ok {
		m.hold("the player count could not be read")
		return
	}
	active, _ := m.waker.Sessions()
	m.counterDistrusted = online == 0 && active > 0
	if online > 0 {
		m.hold(fmt.Sprintf("%d player(s) online", online))
		return
	}

	now := m.now()
	if m.zeroPlayersFrom.IsZero() {
		m.zeroPlayersFrom = now
	}
	// Transfer mode has no counter, so the empty world timer stands in for it.
	if m.cfg.Transfer.Enabled && now.Sub(m.zeroPlayersFrom) < seconds(m.cfg.Sleep.IdleAfter) {
		m.pendingSince = time.Time{}
		return
	}

	if m.pendingSince.IsZero() {
		m.pendingSince = now
		logging.Infof("Nobody is playing, checking once more in %ds before going to %s",
			m.cfg.Sleep.ConfirmDelay, m.cfg.Sleep.Action)
		return
	}
	if now.Sub(m.pendingSince) < seconds(m.cfg.Sleep.ConfirmDelay) {
		return
	}
	m.sleepServer(ctx)
}

// Forgets every timer, so a server that came back does not sleep on a stale one.
func (m *SleepMonitor) hold(reason string) {
	// Logged on change only, this ticks every 30 seconds.
	if !m.pendingSince.IsZero() {
		logging.Infof("Sleep cancelled, %s", reason)
	} else if reason != m.lastReason {
		logging.Infof("Not sleeping, %s", reason)
	}
	m.pendingSince = time.Time{}
	m.zeroPlayersFrom = time.Time{}
	m.lastReason = reason
}

// Only decides whether the server is worth an SSH round trip.
func (m *SleepMonitor) looksIdle() (bool, string) {
	if m.cfg.Transfer.Enabled {
		return true, ""
	}

	active, lastEnd := m.waker.Sessions()
	if active > 0 {
		// Past the trust window, or once the server contradicted it, ask anyway.
		if m.counterDistrusted || !m.within(m.lastPlayerQuery, int(maxCounterTrust.Seconds())) {
			return true, ""
		}
		return false, fmt.Sprintf("%d player(s) are connected through the watcher", active)
	}
	if lastEnd.IsZero() {
		return true, ""
	}
	if m.now().Sub(lastEnd) < seconds(m.cfg.Sleep.IdleAfter) {
		return false, "the last player left less than idle_after ago"
	}
	return true, ""
}

// Not ok means the answer was unreadable, never that the server is empty.
func (m *SleepMonitor) playersOnline(ctx context.Context) (int, bool) {
	m.lastPlayerQuery = m.now()

	// A stopped container has nobody on it, which saves the second round trip.
	state, err := m.runVerb(ctx, remoteVerbStatus)
	if err != nil {
		logging.Warnf("Sleep monitor cannot read the container state: %v", err)
		return 0, false
	}
	if strings.TrimSpace(state) != "running" {
		return 0, true
	}

	out, err := m.runVerb(ctx, remoteVerbPlayers)
	if err != nil {
		logging.Warnf("Sleep monitor cannot read the player count: %v", err)
		return 0, false
	}
	online, ok := parsePlayerCount(out)
	if !ok {
		logging.Warnf("Sleep monitor cannot read a player count from %q", logging.Sanitize(out, 60))
	}
	return online, ok
}

func (m *SleepMonitor) sleepServer(ctx context.Context) {
	m.pendingSince = time.Time{}
	m.zeroPlayersFrom = time.Time{}
	m.lastReason = ""

	actionCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// Suspend resumes the process, hibernate and shutdown do not, so save first.
	if m.cfg.Sleep.Action == "hibernate" || m.cfg.Sleep.Action == "shutdown" {
		logging.Infof("Stopping the container before %s so the world is saved", m.cfg.Sleep.Action)
		if err := m.stopContainer(actionCtx); err != nil {
			logging.Errorf("Not sleeping, the container would not stop: %v", err)
			return
		}
	}

	logging.Infof("Sending the server PC to %s", m.cfg.Sleep.Action)
	// The connection dies mid command as the machine goes down, that is success.
	if _, err := m.runVerb(actionCtx, remoteVerbSleep); err != nil {
		logging.Infof("Sleep command returned %v, normal when the PC goes down mid connection", err)
	}
}

func (m *SleepMonitor) within(mark time.Time, limit int) bool {
	if mark.IsZero() {
		return false
	}
	return m.now().Sub(mark) < seconds(limit)
}

func seconds(count int) time.Duration {
	return time.Duration(count) * time.Second
}
