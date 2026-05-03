package server

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/destinations"
)

// fakePollerSettleTimeout is the upper bound a test waits for one
// poll tick to land. Three intervals is enough slack for goroutine
// scheduling on a busy CI runner without making the test feel slow.
const fakePollerSettleTimeout = 600 * time.Millisecond

// TestMirrorStatusPoller_TickFiresCheckPerDestination — the load-
// bearing assertion: after one tick the check fn must have been
// called for every destination the list fn returned. Two
// destinations exercise the stagger path between iterations.
func TestMirrorStatusPoller_TickFiresCheckPerDestination(t *testing.T) {
	dests := []repoDestination{
		{Repo: "alpha", Dest: &destinations.Destination{ID: "a1"}},
		{Repo: "beta", Dest: &destinations.Destination{ID: "b1"}},
	}
	type call struct{ Repo, ID string }
	var (
		mu    sync.Mutex
		calls []call
	)
	check := func(_ context.Context, repo, id string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{Repo: repo, ID: id})
	}
	// Tiny interval so the test doesn't wait the production default.
	p := newMirrorStatusPoller(60*time.Millisecond,
		func() []repoDestination { return dests }, check)
	defer p.Stop()

	deadline := time.Now().Add(fakePollerSettleTimeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= len(dests) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := append([]call(nil), calls...)
	mu.Unlock()
	if len(got) < len(dests) {
		t.Fatalf("want at least %d check calls, got %d (%v)", len(dests), len(got), got)
	}
	// Order matches list output (the stagger keeps it serial).
	if got[0].Repo != "alpha" || got[1].Repo != "beta" {
		t.Errorf("call order: want alpha then beta, got %v", got)
	}
}

// TestMirrorStatusPoller_StopReapsGoroutine — Stop must return
// promptly AND no further checks must fire after it returns. Without
// the second assertion a buggy Stop that just unblocks `done` but
// leaves the goroutine running would silently regress.
func TestMirrorStatusPoller_StopReapsGoroutine(t *testing.T) {
	var calls atomic.Int64
	dests := []repoDestination{
		{Repo: "x", Dest: &destinations.Destination{ID: "1"}},
	}
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests },
		func(_ context.Context, _, _ string) { calls.Add(1) })

	// Wait for at least one tick to confirm the loop is live before
	// asserting Stop reaps it; otherwise a Stop on a never-ticked
	// poller would pass trivially.
	deadline := time.Now().Add(fakePollerSettleTimeout)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("precondition: poller should have ticked at least once before Stop")
	}

	stopDone := make(chan struct{})
	go func() {
		p.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s; poller goroutine leaked")
	}

	// Snapshot, wait > one interval, snapshot again. No change means
	// no goroutine is still running ticks behind our back.
	before := calls.Load()
	time.Sleep(120 * time.Millisecond)
	after := calls.Load()
	if after != before {
		t.Errorf("checks fired after Stop: before=%d after=%d", before, after)
	}
}

// TestMirrorStatusPoller_SurvivesPanicInCheck — a panic inside one
// destination's check must NOT kill the poller. Otherwise a single
// latent bug (nil pointer in refreshRemoteStatus, etc.) would
// silently stop every status refresh until the next process restart.
// Mirrors the same defensive-engineering fence the mirror_worker
// already carries for its push fan-out.
func TestMirrorStatusPoller_SurvivesPanicInCheck(t *testing.T) {
	var calls atomic.Int64
	dests := []repoDestination{
		{Repo: "x", Dest: &destinations.Destination{ID: "1"}},
	}
	check := func(_ context.Context, _, _ string) {
		n := calls.Add(1)
		if n == 1 {
			panic("synthetic boom on first call")
		}
	}
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests }, check)
	defer p.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("poller should survive panic and tick again; got %d call(s)", got)
	}
}

// TestMirrorStatusPoller_StopInterruptsInFlightCheck — locks in the
// Save-button-hangs fix. Before this slice, Stop() blocked until the
// running check returned, which in production meant up to a 2-minute
// wait per stuck destination because executeLsRemote sets a 2-minute
// per-check timeout. The fix derives every per-check ctx from a
// poller-lifetime ctx that Stop cancels, so a stuck ls-remote (or
// any check that respects ctx.Done) is interrupted promptly.
func TestMirrorStatusPoller_StopInterruptsInFlightCheck(t *testing.T) {
	dests := []repoDestination{
		{Repo: "x", Dest: &destinations.Destination{ID: "1"}},
	}
	checkRunning := make(chan struct{})
	check := func(ctx context.Context, _, _ string) {
		// Tell the test we're inside the check, then block until ctx
		// cancels (mimics executeLsRemote.exec.CommandContext) or
		// the global escape hatch fires.
		select {
		case checkRunning <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Errorf("check still running after 5s; Stop did not propagate cancellation")
		}
	}
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests }, check)

	select {
	case <-checkRunning:
	case <-time.After(fakePollerSettleTimeout):
		t.Fatal("first check did not begin within settle timeout")
	}

	stopDone := make(chan struct{})
	stopStarted := time.Now()
	go func() {
		p.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Stop did not return within 500ms while a check was in flight; took %v",
			time.Since(stopStarted))
	}
	if took := time.Since(stopStarted); took > 200*time.Millisecond {
		t.Errorf("Stop took %v; want < 200ms (cancellation should be immediate)", took)
	}
}

// TestMirrorStatusPoller_HeartbeatRecorded — Snapshot must reflect
// the most recent tick: LastTickAt advances, LastTickError stays
// empty on a clean tick, LastTickDuration is non-zero. Pins the
// recordTick wiring so a refactor that drops one of the three
// breaks the test, not silently the dashboard.
func TestMirrorStatusPoller_HeartbeatRecorded(t *testing.T) {
	dests := []repoDestination{
		{Repo: "x", Dest: &destinations.Destination{ID: "1"}},
	}
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests },
		func(_ context.Context, _, _ string) {
			// Take a measurable amount of time so duration > 0.
			time.Sleep(2 * time.Millisecond)
		})
	defer p.Stop()

	deadline := time.Now().Add(fakePollerSettleTimeout)
	for time.Now().Before(deadline) {
		snap := p.Snapshot()
		if !snap.LastTickAt.IsZero() {
			if snap.LastTickError != "" {
				t.Errorf("clean tick should leave LastTickError empty, got %q", snap.LastTickError)
			}
			if snap.LastTickDuration <= 0 {
				t.Errorf("LastTickDuration should be positive, got %v", snap.LastTickDuration)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Snapshot.LastTickAt never advanced from zero")
}

// TestMirrorStatusPoller_HeartbeatCapturesPanic — the panic path
// must still update LastTickAt (proving the goroutine survived)
// AND populate LastTickError with the panic message (proving the
// admin would see it). Without LastTickError, an admin staring at
// the dashboard would see a heartbeat that's still ticking and
// have no way to know a check is broken.
func TestMirrorStatusPoller_HeartbeatCapturesPanic(t *testing.T) {
	dests := []repoDestination{
		{Repo: "x", Dest: &destinations.Destination{ID: "1"}},
	}
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests },
		func(_ context.Context, _, _ string) {
			panic("synthetic heartbeat-test boom")
		})
	defer p.Stop()

	deadline := time.Now().Add(fakePollerSettleTimeout)
	for time.Now().Before(deadline) {
		snap := p.Snapshot()
		if snap.LastTickError != "" {
			if snap.LastTickAt.IsZero() {
				t.Error("LastTickAt should be set even on panic ticks")
			}
			if !strings.Contains(snap.LastTickError, "synthetic heartbeat-test boom") {
				t.Errorf("LastTickError should carry the panic message, got %q", snap.LastTickError)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("LastTickError never populated despite check panicking on every tick")
}

// TestMirrorStatusPoller_SkipsTrailingStagger — calls back-to-back
// list functions; the stagger skip on the final iteration means tick
// returns close to N*0 + (N-1)*stagger ms, not N*stagger. Without
// the skip, a one-destination tick would always idle for stagger ms
// after the only check, slowing tests AND wasting a poll-window
// budget in production.
func TestMirrorStatusPoller_SkipsTrailingStagger(t *testing.T) {
	dests := []repoDestination{
		{Repo: "only", Dest: &destinations.Destination{ID: "1"}},
	}
	checked := make(chan struct{}, 1)
	p := newMirrorStatusPoller(40*time.Millisecond,
		func() []repoDestination { return dests },
		func(_ context.Context, _, _ string) {
			select {
			case checked <- struct{}{}:
			default:
			}
		})
	defer p.Stop()

	// Wait for the first check, then time how long until a second
	// tick lands. With the trailing-sleep bug, the loop idles for
	// 250ms (the stagger) before the next tick fires; with the fix
	// the next tick should land within ~one interval (40ms) plus
	// goroutine scheduling slop.
	select {
	case <-checked:
	case <-time.After(fakePollerSettleTimeout):
		t.Fatal("first check did not fire within settle timeout")
	}
	start := time.Now()
	select {
	case <-checked:
	case <-time.After(fakePollerSettleTimeout):
		t.Fatal("second check did not fire within settle timeout")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("trailing stagger bites: second tick took %v (want < 200ms)", elapsed)
	}
}
