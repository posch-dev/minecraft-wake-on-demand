package proxy

import (
	"net"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/boot"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/serverinfo"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
)

func addrFor(t *testing.T, hostPort string) net.Addr {
	t.Helper()
	addr, err := net.ResolveTCPAddr("tcp", hostPort)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestConnectionLimiterCapsTotal(t *testing.T) {
	limiter := NewConnectionLimiter("test", 2, 8)

	for i := range 2 {
		if !limiter.Acquire(addrFor(t, "10.0.0.1:5000")) {
			t.Fatalf("acquire %d should have succeeded", i)
		}
	}
	if limiter.Acquire(addrFor(t, "10.0.0.2:5000")) {
		t.Error("third acquire should have been refused")
	}

	limiter.Release(addrFor(t, "10.0.0.1:5000"))
	if !limiter.Acquire(addrFor(t, "10.0.0.2:5000")) {
		t.Error("a freed slot should be reusable")
	}
}

func TestConnectionLimiterCapsPerIP(t *testing.T) {
	limiter := NewConnectionLimiter("test", 100, 2)

	for i := range 2 {
		if !limiter.Acquire(addrFor(t, "10.0.0.1:5000")) {
			t.Fatalf("acquire %d should have succeeded", i)
		}
	}
	if limiter.Acquire(addrFor(t, "10.0.0.1:6000")) {
		t.Error("third connection from the same IP should have been refused")
	}
	if !limiter.Acquire(addrFor(t, "10.0.0.2:5000")) {
		t.Error("a different IP should still get a slot")
	}
}

func TestConnectionLimiterForgetsIdleAddresses(t *testing.T) {
	limiter := NewConnectionLimiter("test", 10, 2)
	addr := addrFor(t, "10.0.0.1:5000")

	limiter.Acquire(addr)
	limiter.Release(addr)
	if limiter.InUse() != 0 {
		t.Errorf("in use = %d, want 0", limiter.InUse())
	}
	if len(limiter.byIP) != 0 {
		t.Errorf("byIP still holds %d entries, it should not grow without bound", len(limiter.byIP))
	}
}

func TestConnectionLimiterSetMaxTakesEffect(t *testing.T) {
	limiter := NewConnectionLimiter("test", 1, 8)
	if !limiter.Acquire(addrFor(t, "10.0.0.1:5000")) {
		t.Fatal("first acquire should have succeeded")
	}
	if limiter.Acquire(addrFor(t, "10.0.0.2:5000")) {
		t.Fatal("second acquire should have been refused")
	}

	limiter.SetMax(2)
	if !limiter.Acquire(addrFor(t, "10.0.0.2:5000")) {
		t.Error("raising the limit should free a slot")
	}
}

func TestLoginLimitFollowsLearnedPlayerSlots(t *testing.T) {
	cfg := testsupport.SleepingConfig(t)
	cfg.MOTD.MaxPlayers = 10
	waker := boot.NewWaker(cfg)
	h := NewHandler(cfg, waker)

	if got := h.loginConnections.max; got != 10 {
		t.Errorf("login limit = %d, want motd.max_players 10 before the server was probed", got)
	}
	if got := h.statusConnections.max; got != minStatusConnections {
		t.Errorf("status limit = %d, want the floor of %d", got, minStatusConnections)
	}

	waker.SetInfo(&serverinfo.Info{Name: "1.21.4", Protocol: 769, MaxPlayers: 40})
	h.refreshConnectionLimits()

	if got := h.loginConnections.max; got != 40 {
		t.Errorf("login limit = %d, want the learned 40", got)
	}
	if got := h.statusConnections.max; got != 200 {
		t.Errorf("status limit = %d, want 40*%d", got, statusPerLogin)
	}
}

func TestConfiguredLoginLimitWinsOverLearnedSlots(t *testing.T) {
	cfg := testsupport.SleepingConfig(t)
	cfg.Limits.MaxLogins = 3
	waker := boot.NewWaker(cfg)
	waker.SetInfo(&serverinfo.Info{Name: "1.21.4", Protocol: 769, MaxPlayers: 40})
	h := NewHandler(cfg, waker)

	if got := h.loginConnections.max; got != 3 {
		t.Errorf("login limit = %d, want the configured 3", got)
	}
	if got := h.statusConnections.max; got != minStatusConnections {
		t.Errorf("status limit = %d, want the floor of %d", got, minStatusConnections)
	}
}
