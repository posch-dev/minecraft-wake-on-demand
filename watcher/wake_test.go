package main

import (
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
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
