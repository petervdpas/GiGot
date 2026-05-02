package server

import (
	"testing"
	"time"
)

// TestLoadTracker_BeginEndCounter pins the basic invariant: Begin
// increments the in-flight counter, End decrements it, Snapshot
// reports the live value.
func TestLoadTracker_BeginEndCounter(t *testing.T) {
	lt := newLoadTracker()
	if got := lt.Snapshot().InFlight; got != 0 {
		t.Fatalf("fresh tracker should be 0, got %d", got)
	}

	a := lt.Begin()
	b := lt.Begin()
	if got := lt.Snapshot().InFlight; got != 2 {
		t.Errorf("after 2 Begins: want 2 in-flight, got %d", got)
	}
	lt.End(a)
	if got := lt.Snapshot().InFlight; got != 1 {
		t.Errorf("after 1 End: want 1 in-flight, got %d", got)
	}
	lt.End(b)
	if got := lt.Snapshot().InFlight; got != 0 {
		t.Errorf("after 2 Ends: want 0 in-flight, got %d", got)
	}
}

// TestLoadTracker_LevelByInFlight pins the in-flight ladder against a
// known CPU count: at 1×CPU the level is medium, at 2×CPU it's high,
// below 1×CPU it's low. Uses a synthetic tracker with cpus=4 so the
// thresholds are deterministic regardless of host hardware.
func TestLoadTracker_LevelByInFlight(t *testing.T) {
	lt := newLoadTracker()
	lt.cpus = 4
	if got := lt.Snapshot().Level; got != "low" {
		t.Errorf("idle: want low, got %s", got)
	}

	// Push to medium: in-flight ≥ cpus.
	for i := 0; i < 4; i++ {
		lt.Begin()
	}
	if got := lt.Snapshot().Level; got != "medium" {
		t.Errorf("4 in-flight (cpus=4): want medium, got %s", got)
	}

	// Push to high: in-flight ≥ 2×cpus.
	for i := 0; i < 4; i++ {
		lt.Begin()
	}
	if got := lt.Snapshot().Level; got != "high" {
		t.Errorf("8 in-flight (cpus=4): want high, got %s", got)
	}
}

// TestLoadTracker_LevelByP95 pins the p95-based promotion: at
// p95 > 200 ms (medium) and p95 > 500 ms (high). Uses an injected
// `now` so the samples are inside the 60-second window without
// real-time delays in the test.
func TestLoadTracker_LevelByP95(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	lt := newLoadTracker()
	lt.cpus = 4 // raise the in-flight bar so p95 dominates
	lt.now = func() time.Time { return base }

	// Seed 100 samples: 95 fast (10ms), 5 at the p95 cliff (250ms).
	// p95 falls on the 95th sample (out of 100), which is one of the
	// slow ones → 250 ms → above the medium step (200ms), below the
	// high step (500ms) → medium.
	for i := 0; i < 95; i++ {
		start := lt.Begin()
		lt.now = func() time.Time { return base.Add(10 * time.Millisecond) }
		lt.End(start)
		lt.now = func() time.Time { return base }
	}
	for i := 0; i < 5; i++ {
		start := lt.Begin()
		lt.now = func() time.Time { return base.Add(250 * time.Millisecond) }
		lt.End(start)
		lt.now = func() time.Time { return base }
	}
	snap := lt.Snapshot()
	if snap.Level != "medium" {
		t.Errorf("p95 ~250ms: want medium, got %s p95=%v", snap.Level, snap.P95Ms)
	}

	// Now push the slow tail to 600 ms — should promote to high.
	for i := 0; i < 10; i++ {
		start := lt.Begin()
		lt.now = func() time.Time { return base.Add(600 * time.Millisecond) }
		lt.End(start)
		lt.now = func() time.Time { return base }
	}
	snap = lt.Snapshot()
	if snap.Level != "high" {
		t.Errorf("p95 ~600ms: want high, got %s p95=%v", snap.Level, snap.P95Ms)
	}
}

// TestLoadTracker_WindowExpiry pins that samples older than the
// 60-second window are dropped from the percentile math. Without
// this the rolling-window claim is a lie — old slow tails would
// keep promoting the level forever.
func TestLoadTracker_WindowExpiry(t *testing.T) {
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	lt := newLoadTracker()
	lt.cpus = 4
	lt.now = func() time.Time { return base }

	// Old slow sample (well outside 60s window, 5 minutes old).
	oldStart := lt.Begin()
	lt.now = func() time.Time { return base.Add(-5 * time.Minute) }
	lt.End(oldStart)

	// Anchor "now" to base; the only sample's timestamp is ancient.
	lt.now = func() time.Time { return base }
	snap := lt.Snapshot()
	if snap.Window != 0 {
		t.Errorf("old sample should be filtered out of the window, got window=%d", snap.Window)
	}
	if snap.Level != "low" {
		t.Errorf("expired samples shouldn't promote level, got %s", snap.Level)
	}
}

// TestLoadTracker_PercentileEmpty confirms the percentile helper
// returns 0 cleanly for an empty slice — Snapshot uses this so the
// JSON shape stays valid (no NaN) on a fresh tracker.
func TestLoadTracker_PercentileEmpty(t *testing.T) {
	if got := percentileDur(nil, 95); got != 0 {
		t.Errorf("empty percentile: want 0, got %v", got)
	}
}
