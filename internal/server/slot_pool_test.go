package server

import (
	"sync"
	"testing"
)

// TestSlotPool_BasicAcquireRelease pins the contract: TryAcquire
// succeeds while in-use < capacity, fails at capacity, and Release
// reopens a slot. The most basic invariant of the admission gate.
func TestSlotPool_BasicAcquireRelease(t *testing.T) {
	p := newSlotPool(2)
	if !p.TryAcquire() {
		t.Fatal("first acquire should succeed")
	}
	if !p.TryAcquire() {
		t.Fatal("second acquire should succeed (at cap)")
	}
	if p.TryAcquire() {
		t.Fatal("third acquire should fail (over cap)")
	}
	p.Release()
	if !p.TryAcquire() {
		t.Fatal("acquire after release should succeed")
	}
}

// TestSlotPool_Snapshot pins the snapshot wire shape against a
// deterministic sequence so the load endpoint and the admin UI
// see the same numbers the gate is acting on.
func TestSlotPool_Snapshot(t *testing.T) {
	p := newSlotPool(5)
	if u, c := p.Snapshot(); u != 0 || c != 5 {
		t.Errorf("fresh: want 0/5, got %d/%d", u, c)
	}
	p.TryAcquire()
	p.TryAcquire()
	p.TryAcquire()
	if u, c := p.Snapshot(); u != 3 || c != 5 {
		t.Errorf("after 3 acquires: want 3/5, got %d/%d", u, c)
	}
	p.Release()
	if u, c := p.Snapshot(); u != 2 || c != 5 {
		t.Errorf("after one release: want 2/5, got %d/%d", u, c)
	}
}

// TestSlotPool_Resize_Grow pins the operator-tunable path: bumping
// capacity while requests are in flight should immediately admit
// the next acquire — the new capacity takes effect for the gate's
// future decisions, in-flight slots are unaffected.
func TestSlotPool_Resize_Grow(t *testing.T) {
	p := newSlotPool(1)
	p.TryAcquire()
	if p.TryAcquire() {
		t.Fatal("at cap=1, second acquire must fail")
	}
	prev := p.Resize(3)
	if prev != 1 {
		t.Errorf("Resize should return previous cap 1, got %d", prev)
	}
	if !p.TryAcquire() {
		t.Error("after grow to 3, second acquire should succeed")
	}
	if !p.TryAcquire() {
		t.Error("after grow to 3, third acquire should succeed")
	}
	if p.TryAcquire() {
		t.Error("after grow to 3 with 3 in use, fourth acquire must fail")
	}
}

// TestSlotPool_Resize_Shrink pins the "we're already overloaded,
// close the gate" semantics: in-flight slots run to completion on
// the old capacity, but no new slots are admitted until enough
// Releases bring inUse back under the new (smaller) cap.
func TestSlotPool_Resize_Shrink(t *testing.T) {
	p := newSlotPool(5)
	for i := 0; i < 4; i++ {
		p.TryAcquire()
	}
	p.Resize(2)
	if u, c := p.Snapshot(); u != 4 || c != 2 {
		t.Errorf("immediately after shrink: want inUse=4, cap=2, got %d/%d", u, c)
	}
	if p.TryAcquire() {
		t.Error("shrink with inUse > new cap should reject new acquires")
	}
	p.Release()
	p.Release()
	p.Release()
	if u, c := p.Snapshot(); u != 1 || c != 2 {
		t.Errorf("after 3 releases: want 1/2, got %d/%d", u, c)
	}
	if !p.TryAcquire() {
		t.Error("after releases bring inUse below new cap, acquire should succeed")
	}
}

// TestSlotPool_Resize_ClampsToOne pins the lower bound: zero-or-
// negative resize requests get clamped to 1 so a typo can't lock
// every push out of the system. Validation happens in the handler
// too, but the pool itself is the last line of defense.
func TestSlotPool_Resize_ClampsToOne(t *testing.T) {
	p := newSlotPool(5)
	p.Resize(0)
	if _, c := p.Snapshot(); c != 1 {
		t.Errorf("Resize(0) should clamp to 1, got %d", c)
	}
	p.Resize(-3)
	if _, c := p.Snapshot(); c != 1 {
		t.Errorf("Resize(-3) should clamp to 1, got %d", c)
	}
}

// TestSlotPool_ConcurrentAcquireRelease pins thread safety under
// a small fan-out. With cap=10 and 100 concurrent acquire→release
// pairs, the in-use count must end at zero — no off-by-one races.
func TestSlotPool_ConcurrentAcquireRelease(t *testing.T) {
	p := newSlotPool(10)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.TryAcquire() {
				p.Release()
			}
		}()
	}
	wg.Wait()
	if u, _ := p.Snapshot(); u != 0 {
		t.Errorf("after 100 acquire→release pairs, want inUse=0, got %d", u)
	}
}
