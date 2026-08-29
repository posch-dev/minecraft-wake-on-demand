package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
)

// Stands in for the server so the state machine can be driven without SSH.
type fakeServerAnswers struct {
	containerState string
	playerOutput   string
	playersErr     error
	sleepCalls     int
	stopCalls      int
}

func sleepMonitorFor(t *testing.T, answers *fakeServerAnswers, transfer bool) (*SleepMonitor, *Waker, *time.Time) {
	t.Helper()
	cfg := sleepingConfig()
	cfg.Server.RemoteHelper = true
	cfg.Sleep.Enabled = true
	cfg.Sleep.Action = "suspend"
	cfg.Sleep.IdleAfter = 900
	cfg.Sleep.ConfirmDelay = 60
	cfg.Sleep.GracePeriod = 900
	cfg.Sleep.PollInterval = 300
	cfg.Transfer.Enabled = transfer
	if transfer {
		cfg.Transfer.Host = "mc.example.org"
	}

	waker := NewWaker(cfg)
	clock := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	monitor := &SleepMonitor{
		cfg:   cfg,
		waker: waker,
		now:   func() time.Time { return clock },
		runVerb: func(_ context.Context, verb string) (string, error) {
			switch verb {
			case remote.RemoteVerbStatus:
				return answers.containerState, nil
			case remote.RemoteVerbPlayers:
				if answers.playersErr != nil {
					return "", answers.playersErr
				}
				return answers.playerOutput, nil
			case remote.RemoteVerbSleep:
				answers.sleepCalls++
				return "", nil
			}
			return "", errors.New("unexpected verb " + verb)
		},
		stopContainer: func(context.Context) error {
			answers.stopCalls++
			return nil
		},
		hostReachable: func(context.Context) bool { return true },
	}
	return monitor, waker, &clock
}

// A player who is connected right now must never be cut off, however long the
// watcher has been running.
func TestSleepMonitorLeavesAConnectedPlayerAlone(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 1 of a max of 20 players online: eliah"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.SessionStarted()

	for range 5 {
		monitor.tick(context.Background())
		*clock = clock.Add(time.Minute)
	}

	if answers.sleepCalls != 0 {
		t.Errorf("the PC was sent to sleep %d times with a player connected", answers.sleepCalls)
	}
}

func TestSleepMonitorWaitsForTheIdleWindow(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Minute)

	monitor.tick(context.Background())

	if !monitor.pendingSince.IsZero() {
		t.Error("sleep should not be pending one minute after the last player left")
	}
	if answers.sleepCalls != 0 {
		t.Errorf("the PC was sent to sleep %d times too early", answers.sleepCalls)
	}
}

func TestSleepMonitorConfirmsBeforeSleeping(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	monitor.tick(context.Background())
	if monitor.pendingSince.IsZero() {
		t.Fatal("an idle server should arm the confirmation")
	}
	if answers.sleepCalls != 0 {
		t.Fatal("the first pass must not sleep, that is what confirm_delay is for")
	}

	*clock = clock.Add(30 * time.Second)
	monitor.tick(context.Background())
	if answers.sleepCalls != 0 {
		t.Fatal("sleeping before confirm_delay elapsed")
	}

	*clock = clock.Add(31 * time.Second)
	monitor.tick(context.Background())
	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1 after confirm_delay", answers.sleepCalls)
	}
}

// Someone joining during the confirmation window has to cancel it.
func TestSleepMonitorCancelsWhenAPlayerReturns(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	monitor.tick(context.Background())
	if monitor.pendingSince.IsZero() {
		t.Fatal("the confirmation should be armed")
	}

	answers.playerOutput = "There are 1 of a max of 20 players online: eliah"
	*clock = clock.Add(90 * time.Second)
	monitor.tick(context.Background())

	if !monitor.pendingSince.IsZero() {
		t.Error("a returning player should cancel the pending sleep")
	}
	if answers.sleepCalls != 0 {
		t.Errorf("sleep calls = %d, want 0", answers.sleepCalls)
	}
}

func TestSleepMonitorRespectsTheGracePeriod(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastSessionEnd = clock.Add(-time.Hour)
	// The PC finished booting a minute ago, the first player is still loading.
	waker.lastBootAt = clock.Add(-time.Minute)

	monitor.tick(context.Background())

	if answers.sleepCalls != 0 || !monitor.pendingSince.IsZero() {
		t.Error("nothing should happen inside the grace period after a wake")
	}
}

// An unreadable answer means we do not know, and not knowing must never be
// treated as an empty server.
func TestSleepMonitorTreatsAnUnreadableCountAsBusy(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "rcon: connection refused"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	for range 5 {
		monitor.tick(context.Background())
		*clock = clock.Add(2 * time.Minute)
	}

	if answers.sleepCalls != 0 {
		t.Errorf("sleep calls = %d, an unreadable count must not send the PC to sleep", answers.sleepCalls)
	}
}

func TestSleepMonitorTreatsAFailedQueryAsBusy(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playersErr: errors.New("rcon unreachable")}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	for range 5 {
		monitor.tick(context.Background())
		*clock = clock.Add(2 * time.Minute)
	}

	if answers.sleepCalls != 0 {
		t.Errorf("sleep calls = %d, a failed query must not send the PC to sleep", answers.sleepCalls)
	}
}

// A stopped container has nobody on it, so that case does not need RCON at all.
func TestSleepMonitorSleepsWhenTheContainerIsStopped(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "exited", playersErr: errors.New("container is not running")}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	monitor.tick(context.Background())
	*clock = clock.Add(2 * time.Minute)
	monitor.tick(context.Background())

	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1 with the container stopped", answers.sleepCalls)
	}
}

// A client that opens a connection and goes quiet would otherwise hold the
// counter above zero forever.
func TestSleepMonitorStopsTrustingAStuckSessionCounter(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-4 * time.Hour)
	waker.SessionStarted()

	// While the last answer is fresh, the counter is believed.
	monitor.lastPlayerQuery = *clock
	monitor.tick(context.Background())
	if !monitor.pendingSince.IsZero() {
		t.Fatal("the counter should be trusted while it is fresh")
	}

	// Past the trust window the server is asked anyway and contradicts it.
	*clock = clock.Add(maxCounterTrust + time.Minute)
	monitor.tick(context.Background())
	if monitor.pendingSince.IsZero() {
		t.Fatal("past the trust window the server answer has to win")
	}

	*clock = clock.Add(2 * time.Minute)
	monitor.tick(context.Background())
	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1", answers.sleepCalls)
	}
}

func TestSleepMonitorStopsTheContainerBeforeHibernating(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	monitor.cfg.Sleep.Action = "hibernate"
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	monitor.tick(context.Background())
	*clock = clock.Add(2 * time.Minute)
	monitor.tick(context.Background())

	if answers.stopCalls != 1 {
		t.Errorf("stop calls = %d, hibernating without saving the world risks it", answers.stopCalls)
	}
	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1", answers.sleepCalls)
	}
}

func TestSleepMonitorDoesNotStopTheContainerBeforeSuspending(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, false)
	waker.lastBootAt = clock.Add(-2 * time.Hour)
	waker.lastSessionEnd = clock.Add(-time.Hour)

	monitor.tick(context.Background())
	*clock = clock.Add(2 * time.Minute)
	monitor.tick(context.Background())

	if answers.stopCalls != 0 {
		t.Errorf("stop calls = %d, suspend resumes the process so the world stays put", answers.stopCalls)
	}
	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1", answers.sleepCalls)
	}
}

// Transfer mode has no session counter, the empty world timer replaces it.
func TestSleepMonitorInTransferModeWaitsOutTheIdleWindow(t *testing.T) {
	answers := &fakeServerAnswers{containerState: "running", playerOutput: "There are 0 of a max of 20 players online:"}
	monitor, waker, clock := sleepMonitorFor(t, answers, true)
	waker.lastBootAt = clock.Add(-2 * time.Hour)

	monitor.tick(context.Background())
	if answers.sleepCalls != 0 {
		t.Fatal("the first empty answer must only start the timer")
	}

	*clock = clock.Add(seconds(monitor.cfg.Sleep.IdleAfter))
	monitor.tick(context.Background())
	*clock = clock.Add(2 * time.Minute)
	monitor.tick(context.Background())

	if answers.sleepCalls != 1 {
		t.Errorf("sleep calls = %d, want 1 once idle_after has passed", answers.sleepCalls)
	}
}

func TestSleepMonitorTickIntervalFollowsTheMode(t *testing.T) {
	proxy, _, _ := sleepMonitorFor(t, &fakeServerAnswers{}, false)
	if got := proxy.tickInterval(); got != proxyTickInterval {
		t.Errorf("proxy mode interval = %v, want %v", got, proxyTickInterval)
	}

	transfer, _, _ := sleepMonitorFor(t, &fakeServerAnswers{}, true)
	if got := transfer.tickInterval(); got != 300*time.Second {
		t.Errorf("transfer mode interval = %v, want the configured 300s", got)
	}
}
