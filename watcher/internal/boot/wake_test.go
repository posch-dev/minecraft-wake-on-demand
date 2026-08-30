package boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/serverinfo"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
)

func TestCooldownWithoutFailuresUsesTheShortGap(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.BootCooldown = 10
	waker := NewWaker(&cfg)

	if waker.cooldownRemaining() != 0 {
		t.Error("a watcher that never booted must not be in cooldown")
	}

	waker.lastAttempt = time.Now()
	remaining := waker.cooldownRemaining()
	if remaining <= 0 || remaining > 10*time.Second {
		t.Errorf("remaining = %v, want just under 10s", remaining)
	}
}

// The gap doubles per failure and stops at boot_max_backoff.
func TestFailureBackoffDoublesAndIsCapped(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.BootCooldown = 10
	cfg.Limits.BootFailureBackoff = 60
	cfg.Limits.BootMaxBackoff = 900
	waker := NewWaker(&cfg)
	waker.lastAttempt = time.Now()

	expected := map[int]time.Duration{
		1: 60 * time.Second,
		2: 120 * time.Second,
		3: 240 * time.Second,
		4: 480 * time.Second,
		5: 900 * time.Second, // 960 would exceed the cap
		6: 900 * time.Second,
	}
	for failures, want := range expected {
		waker.failures = failures
		got := waker.cooldownRemaining()
		// Allow for the time the test itself takes.
		if got > want || got < want-2*time.Second {
			t.Errorf("%d failures gave %v, want about %v", failures, got, want)
		}
	}
}

// The Python version relied on arbitrary precision integers here, a fixed
// width shift would overflow into a negative delay.
func TestBackoffSurvivesAbsurdFailureCounts(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.BootFailureBackoff = 60
	cfg.Limits.BootMaxBackoff = 900
	waker := NewWaker(&cfg)
	waker.lastAttempt = time.Now()

	for _, failures := range []int{30, 31, 64, 1000, 100000} {
		waker.failures = failures
		got := waker.cooldownRemaining()
		if got <= 0 {
			t.Errorf("%d failures gave %v, the gap must never collapse", failures, got)
		}
		if got > 900*time.Second {
			t.Errorf("%d failures gave %v, above the cap", failures, got)
		}
	}
}

func TestBootStateTracksFailuresAndResets(t *testing.T) {
	cfg := config.Default()
	waker := NewWaker(&cfg)

	if waker.Booting() {
		t.Error("a fresh watcher is not booting")
	}
	waker.beginBoot()
	if !waker.Booting() {
		t.Error("beginBoot should mark it as booting")
	}
	waker.endBoot(false)
	if waker.Booting() {
		t.Error("endBoot should clear the flag")
	}
	if waker.failures != 1 {
		t.Errorf("failures = %d, want 1", waker.failures)
	}

	waker.beginBoot()
	waker.endBoot(false)
	if waker.failures != 2 {
		t.Errorf("failures = %d, want 2", waker.failures)
	}

	waker.beginBoot()
	waker.endBoot(true)
	if waker.failures != 0 {
		t.Errorf("a success must reset the counter, got %d", waker.failures)
	}
}

func TestMCAcceptsStatusAgainstARunningServer(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	cfg, waker := wakerFor(server)
	_ = cfg

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("a server that answers a status request counts as ready")
	}

	// It has to send a well formed handshake, not just open a socket.
	handshake, err := mcproto.ParseHandshake(server.Got())
	if err != nil {
		t.Fatalf("the probe sent something unparseable: %v", err)
	}
	if handshake.NextState != 1 {
		t.Errorf("next state = %d, want 1 for a status probe", handshake.NextState)
	}
}

// An open port is not enough, this is the case the probe exists for.
func TestMCAcceptsStatusRejectsASilentPort(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, false, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	if waker.mcAcceptsStatus(ctx) {
		t.Fatal("a port that never answers must not count as ready")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("the probe took %v, it has to give up on its own deadline", elapsed)
	}
}

func TestMCPortReachableIsCached(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.MCPortReachable(ctx, false) {
		t.Fatal("the fake server is listening")
	}
	checkedAt := waker.reachChecked

	// A second call inside the TTL must not probe again.
	if !waker.MCPortReachable(ctx, false) {
		t.Fatal("the cached answer should still be true")
	}
	if !waker.reachChecked.Equal(checkedAt) {
		t.Error("the second call probed again instead of using the cache")
	}

	// The cache is pushed into the past first, two calls microseconds apart
	// can read the same wall clock on a coarse timer.
	waker.reachChecked = time.Now().Add(-time.Hour)
	stale := waker.reachChecked
	if !waker.MCPortReachable(ctx, true) {
		t.Fatal("forced probe should still find the server")
	}
	if !waker.reachChecked.After(stale) {
		t.Error("force did not refresh the cache")
	}
}

// Changing a world's version invalidates what was learned about it, the other
// world keeps its entry.
func TestForgetServerInfoDropsOnlyOneWorld(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	cfg.Worlds.List = []config.World{{Name: "survival"}, {Name: "creative"}}

	cfg.Worlds.Active = "survival"
	serverinfo.Save(&cfg, &serverinfo.Info{Name: "1.21.4", Protocol: 769})
	cfg.Worlds.Active = "creative"
	serverinfo.Save(&cfg, &serverinfo.Info{Name: "26.2", Protocol: 776})

	serverinfo.Forget(&cfg, "survival")

	cfg.Worlds.Active = "survival"
	if got := NewWaker(&cfg).CachedInfo(); got != nil {
		t.Errorf("survival = %+v, want nothing", got)
	}
	cfg.Worlds.Active = "creative"
	if got := NewWaker(&cfg).CachedInfo(); got == nil || got.Protocol != 776 {
		t.Errorf("creative = %+v, want protocol 776", got)
	}
}

// A cache written before worlds is not read, it is replaced by learning again.
func TestServerInfoFromAnOlderVersionIsIgnored(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	old := []byte(`{"name":"1.21.4","protocol":769,"max_players":20}`)
	if err := os.WriteFile(cfg.ServerInfoPath(), old, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := NewWaker(&cfg).CachedInfo(); got != nil {
		t.Errorf("cached = %+v, want nothing", got)
	}

	serverinfo.Save(&cfg, &serverinfo.Info{Name: "26.2", Protocol: 776})
	if got := NewWaker(&cfg).CachedInfo(); got == nil || got.Protocol != 776 {
		t.Errorf("cached = %+v, want protocol 776", got)
	}
}

func TestLoadServerInfoNoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")

	waker := NewWaker(&cfg)
	if waker.CachedInfo() != nil {
		t.Error("expected nil version when no cache file exists")
	}
}

func TestLoadServerInfoCorruptFile(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, ".server-info.json")
	if err := os.WriteFile(cachePath, []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")

	waker := NewWaker(&cfg)
	if waker.CachedInfo() != nil {
		t.Error("expected nil version when cache file is corrupt")
	}
}

func TestLearnServerInfoCachesInMemory(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	cached := waker.CachedInfo()
	if cached == nil {
		t.Fatal("version was not learned from the status response")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v, want name=1.21.4 protocol=769", cached)
	}
}

func TestLearnServerInfoIgnoresEmptyVersion(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	waker.infoMu.Lock()
	waker.info = nil
	waker.infoMu.Unlock()

	response, _ := mcproto.MakeStatusResponse("{\"text\":\"x\"}", 10, 0, "", "", 0)
	waker.learnServerInfo(testsupport.StatusBody(t, response))

	if waker.CachedInfo() != nil {
		t.Error("empty version should not be cached")
	}
}

func TestLearnServerInfoDoesNotUpdateWhenSame(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	first := waker.CachedInfo()
	if first == nil {
		t.Fatal("version should have been learned")
	}

	// Same version and same player slots as the fake server reported.
	response, _ := mcproto.MakeStatusResponse("{\"text\":\"x\"}", 42, 0, "", "1.21.4", 769)
	waker.learnServerInfo(testsupport.StatusBody(t, response))

	cached := waker.CachedInfo()
	if !cached.Updated.Equal(first.Updated) {
		t.Error("learning the same info should not bump Updated")
	}
}

func TestLearnServerInfoPicksUpChangedPlayerSlots(t *testing.T) {
	server := testsupport.StartFakeMCServer(t, true, nil)
	_, waker := wakerFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}
	if got := waker.CachedInfo().MaxPlayers; got != 42 {
		t.Fatalf("max players = %d, want 42", got)
	}

	response, _ := mcproto.MakeStatusResponse("{\"text\":\"x\"}", 60, 0, "", "1.21.4", 769)
	waker.learnServerInfo(testsupport.StatusBody(t, response))

	if got := waker.CachedInfo().MaxPlayers; got != 60 {
		t.Errorf("max players = %d, want 60 after the server raised the slots", got)
	}
}

func TestServerInfoCachesToFile(t *testing.T) {
	dir := t.TempDir()
	server := testsupport.StartFakeMCServer(t, true, nil)
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.Port
	cfg.Server.SSHUser = "tester"
	cfg.WoL.BroadcastAddress = "127.0.0.1"
	cfg.Path = filepath.Join(dir, "config.yml")
	waker := NewWaker(&cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if !waker.mcAcceptsStatus(ctx) {
		t.Fatal("server should be reachable")
	}

	time.Sleep(100 * time.Millisecond)

	waker2 := NewWaker(&cfg)
	cached := waker2.CachedInfo()
	if cached == nil {
		t.Fatal("version was not persisted to disk")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v, want name=1.21.4 protocol=769", cached)
	}
}

func wakerFor(server *testsupport.FakeMCServer) (*config.Config, *Waker) {
	cfg := config.Default()
	cfg.Server.MAC = "AA:BB:CC:DD:EE:FF"
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.Port
	cfg.Server.SSHUser = "tester"
	cfg.WoL.BroadcastAddress = "127.0.0.1"
	return &cfg, NewWaker(&cfg)
}
