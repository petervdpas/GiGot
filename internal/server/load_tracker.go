package server

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// loadTracker is GiGot's "how loaded am I right now" gauge. Two
// inputs:
//   - in-flight count: incremented by Begin(), decremented by End().
//     Long-running requests (git push / pull) hold the counter up.
//   - rolling sample window: completed-request durations from the
//     last `windowDur`. p95 / p99 over this window hint at recent
//     tail latency.
//
// Snapshot() classifies these into a coarse `low` / `medium` / `high`
// label that the load-header middleware reflects on every response
// as `X-GiGot-Load`. The intended consumer is Formidable: local-first
// writes never block on GiGot, but Formidable can read the header
// after each background sync to (a) surface a "server busy" hint
// to the user and (b) back off retry frequency / skip optional
// mirror dispatches when load is high.
//
// Design notes:
//   - Bounded memory: capacity-N ring buffer of samples; oldest
//     samples expire when overwritten (after `capacity` ends) AND
//     are filtered out of percentile math when older than `windowDur`.
//   - One lock for everything. Tracker work is microseconds; locking
//     simplifies reasoning and the mutex never sees real contention
//     because git requests are slow relative to the lock window.
//   - Thresholds are conservative — calibrated so a typical
//     idle-to-light-load instance reports `low` and a single-core
//     B1 saturating its CPU reports `medium`. `high` is reserved
//     for "Formidable should defer optional work."

const (
	loadWindowDur     = 60 * time.Second
	loadSampleCap     = 256
	loadP95MediumStep = 200 * time.Millisecond
	loadP95HighStep   = 500 * time.Millisecond
)

type loadSample struct {
	at  time.Time
	dur time.Duration
}

type loadTracker struct {
	mu       sync.Mutex
	inFlight int
	samples  []loadSample // ring buffer, capacity loadSampleCap
	next     int          // next write index
	cpus     int
	now      func() time.Time // overridable in tests
}

func newLoadTracker() *loadTracker {
	cpus := runtime.NumCPU()
	if cpus < 1 {
		cpus = 1
	}
	return &loadTracker{
		samples: make([]loadSample, loadSampleCap),
		cpus:    cpus,
		now:     time.Now,
	}
}

// Begin records the start of a tracked operation. Returns the start
// time the caller must thread through to End. Splitting Begin / End
// into separate calls (instead of a Wrap closure) lets handlers that
// hijack the connection or stream chunked responses still close the
// bracket at the right moment.
func (t *loadTracker) Begin() time.Time {
	t.mu.Lock()
	t.inFlight++
	t.mu.Unlock()
	return t.now()
}

// End closes the bracket opened by Begin and records the elapsed
// duration into the rolling window.
func (t *loadTracker) End(start time.Time) {
	d := t.now().Sub(start)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inFlight > 0 {
		t.inFlight--
	}
	t.samples[t.next] = loadSample{at: t.now(), dur: d}
	t.next = (t.next + 1) % loadSampleCap
}

// LoadSnapshot is the JSON shape returned by GET /api/health/load
// and (the Level field only) reflected on every response via the
// `X-GiGot-Load` header.
type LoadSnapshot struct {
	Level    string  `json:"level" example:"low" enums:"low,medium,high"`
	InFlight int     `json:"in_flight" example:"3"`
	P95Ms    float64 `json:"p95_ms" example:"42.5"`
	P99Ms    float64 `json:"p99_ms" example:"118.0"`
	// Window is the count of samples that fell inside the rolling
	// 60-second window for this snapshot. Useful for debugging
	// "why is load reported as low when I just pushed?" — if Window
	// is 0, the percentiles are 0 because no samples are in scope yet.
	Window int `json:"window_count" example:"24"`
	// PushSlotInUse / PushSlotCapacity report the admission-gate
	// state. Pushes are rejected with 429 + `Retry-After` when
	// in_use ≥ capacity. Read these to decide whether the gate is
	// actively rejecting traffic vs. just running near saturation.
	PushSlotInUse    int `json:"push_slot_in_use" example:"3"`
	PushSlotCapacity int `json:"push_slot_capacity" example:"10"`
}

// Snapshot reads the current state and computes a level. Cheap
// enough to call on every request — O(loadSampleCap) sort under
// the lock, well under a microsecond at our cap.
func (t *loadTracker) Snapshot() LoadSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := t.now().Add(-loadWindowDur)
	durs := make([]time.Duration, 0, loadSampleCap)
	for _, s := range t.samples {
		if s.at.IsZero() || s.at.Before(cutoff) {
			continue
		}
		durs = append(durs, s.dur)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })

	p95 := percentileDur(durs, 95)
	p99 := percentileDur(durs, 99)

	level := classifyLoad(t.inFlight, t.cpus, p95)

	return LoadSnapshot{
		Level:    level,
		InFlight: t.inFlight,
		P95Ms:    msFromDur(p95),
		P99Ms:    msFromDur(p99),
		Window:   len(durs),
	}
}

// classifyLoad maps the two raw signals (in-flight count + recent
// p95) to a coarse string. Either signal alone can promote the
// level — saturating CPUs without slow tails still means "the host
// is busy, don't pile on," and slow tails without high in-flight
// means "something is making requests slow even when the queue is
// shallow." The thresholds err on the side of NOT crying wolf —
// `medium` should mean "noticeable" and `high` should mean
// "Formidable should defer optional work."
func classifyLoad(inFlight, cpus int, p95 time.Duration) string {
	if inFlight >= 2*cpus || p95 > loadP95HighStep {
		return "high"
	}
	if inFlight >= cpus || p95 > loadP95MediumStep {
		return "medium"
	}
	return "low"
}

func percentileDur(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func msFromDur(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}
