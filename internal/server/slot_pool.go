package server

import "sync"

// slotPool is the admission gate for concurrent git-receive-pack
// handlers. Mutex-based (not channel-based) so the capacity can be
// resized at runtime by the admin /admin/limits PATCH endpoint —
// shrinking a buffered channel would leave existing tokens stranded.
//
// Contract:
//   - TryAcquire returns true exactly when the in-use count is
//     strictly below capacity, and atomically increments the count.
//     A false return means "all slots full, tell the client to
//     retry later."
//   - Release decrements the count. Calling Release without a
//     matching successful TryAcquire is a programmer bug; the pool
//     defends against negative counts but doesn't surface the
//     misuse — Begin/End in the tracker uses the same shape.
//   - Resize changes capacity at runtime. Callers in flight finish
//     normally; new acquires are gated by the new capacity. If
//     capacity shrinks below the current in-use count, the gate is
//     effectively closed until enough Releases bring inUse back
//     under the new cap. That's fine — the system is already
//     overloaded; closing the gate is the right behaviour.
//   - Snapshot returns (inUse, capacity) for the load endpoint.

type slotPool struct {
	mu       sync.Mutex
	inUse    int
	capacity int
}

func newSlotPool(capacity int) *slotPool {
	if capacity < 1 {
		capacity = 1
	}
	return &slotPool{capacity: capacity}
}

func (p *slotPool) TryAcquire() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inUse >= p.capacity {
		return false
	}
	p.inUse++
	return true
}

func (p *slotPool) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inUse > 0 {
		p.inUse--
	}
}

// Resize updates the capacity. Returns the previous capacity so
// callers (e.g. the PATCH handler) can log the transition. Capacity
// is clamped to at least 1 — a zero-slot pool would block all
// pushes forever, which is never the right operational answer.
func (p *slotPool) Resize(n int) int {
	if n < 1 {
		n = 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.capacity
	p.capacity = n
	return prev
}

// Snapshot returns the current (inUse, capacity) under one lock —
// avoids a torn read where the load endpoint shows inUse=11 cap=10
// (or similar) during a Resize.
func (p *slotPool) Snapshot() (inUse, capacity int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inUse, p.capacity
}
