package server

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// mirrorStatusPollerSnapshot is the read-side view of the poller's
// runtime state. Returned by Snapshot under RLock so callers (the
// /api/admin/mirror GET handler in particular) get a coherent
// triple — every field reflects the same recordTick call.
//
// LastTickAt is zero until the first tick completes; callers should
// nil-check before formatting a "last poll: X ago" line.
// LastTickError is empty on success and carries the panic message
// when tickSafe's recover fired during the last tick. A populated
// LastTickError plus an advancing LastTickAt means the poller is
// alive but a check is breaking; an unchanged LastTickAt for >2x
// interval means the goroutine itself has stalled.
type mirrorStatusPollerSnapshot struct {
	LastTickAt       time.Time
	LastTickDuration time.Duration
	LastTickError    string
}

// mirrorStatusPollPerCheckTimeout caps each ls-remote inside the poll
// loop. Same shape as mirrorPushPerDestTimeout — one stuck remote can
// not jam the loop for longer than this, so the rest of the
// destinations still get checked on the same tick.
const mirrorStatusPollPerCheckTimeout = mirrorLsRemoteTimeout

// mirrorStatusPollStagger spaces out checks within one tick so we
// don't fire every ls-remote in the same millisecond. Enough slack
// that 50 destinations still complete one pass well inside a 10-min
// tick, but small enough that a flaky upstream doesn't dominate.
const mirrorStatusPollStagger = 250 * time.Millisecond

// mirrorStatusPoller is the background ticker that re-checks each
// enabled destination's remote-status on a schedule. One goroutine
// per process (deliberately not HA — same caveat as mirror_worker).
// Stops when Stop is called or the context returned by Start is
// cancelled.
type mirrorStatusPoller struct {
	interval time.Duration
	list     func() []repoDestination
	check    func(ctx context.Context, repo, id string)
	stop     chan struct{}
	done     chan struct{}

	// ctx is the poller-lifetime context. Every per-check ctx is
	// derived from it via context.WithTimeout, so a Stop() that
	// fires while a check is mid-flight cancels the derived ctx
	// immediately — exec.CommandContext inside executeLsRemote
	// then SIGKILLs the running git process. Without this,
	// Stop() blocks until the slowest stuck check hits its
	// 2-minute per-check timeout, which surfaces in the admin UI
	// as a "Save" button that appears to hang while a previous
	// poll tick is still running against an unreachable mirror.
	ctx    context.Context
	cancel context.CancelFunc

	// Heartbeat. Written by recordTick (called from tickSafe on both
	// success and panic-recovery paths), read by Snapshot under
	// RLock. Independent of the per-destination state on
	// destinations.Destination — those are individual results;
	// these answer "is the loop itself alive."
	snapMu           sync.RWMutex
	lastTickAt       time.Time
	lastTickDuration time.Duration
	lastTickErr      string
}

func newMirrorStatusPoller(
	interval time.Duration,
	list func() []repoDestination,
	check func(ctx context.Context, repo, id string),
) *mirrorStatusPoller {
	ctx, cancel := context.WithCancel(context.Background())
	p := &mirrorStatusPoller{
		interval: interval,
		list:     list,
		check:    check,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
	go p.run()
	return p
}

// run waits one full interval before the first tick (so tests don't
// race against an immediate fire and so a fresh-boot server doesn't
// shell out N times before it's even served a request) and then loops
// until Stop. Panics inside one tick are caught so a latent bug in
// refreshRemoteStatus doesn't kill the poller for the lifetime of the
// process.
func (p *mirrorStatusPoller) run() {
	defer close(p.done)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.tickSafe()
		}
	}
}

func (p *mirrorStatusPoller) tickSafe() {
	started := time.Now()
	defer func() {
		if r := recover(); r != nil {
			p.recordTick(started, time.Since(started), fmt.Sprintf("%v", r))
			log.Printf("mirror-status: poll tick panic: %v\n%s", r, debug.Stack())
			return
		}
		p.recordTick(started, time.Since(started), "")
	}()
	p.tick()
}

// recordTick is the single write point for the heartbeat fields.
// Called from both the success and panic-recovery paths in
// tickSafe. Held under Lock so a concurrent Snapshot reader sees
// either the previous tick's values or this one's, never a torn
// mix. Writes are infrequent (one per interval, typically minutes
// apart) so the lock is uncontended in practice.
func (p *mirrorStatusPoller) recordTick(at time.Time, duration time.Duration, errMsg string) {
	p.snapMu.Lock()
	defer p.snapMu.Unlock()
	p.lastTickAt = at
	p.lastTickDuration = duration
	p.lastTickErr = errMsg
}

// Snapshot returns the heartbeat fields under RLock. Returns the
// zero value before the first tick completes — callers should
// IsZero-check LastTickAt before formatting an age. Safe to call
// from any goroutine.
func (p *mirrorStatusPoller) Snapshot() mirrorStatusPollerSnapshot {
	if p == nil {
		return mirrorStatusPollerSnapshot{}
	}
	p.snapMu.RLock()
	defer p.snapMu.RUnlock()
	return mirrorStatusPollerSnapshot{
		LastTickAt:       p.lastTickAt,
		LastTickDuration: p.lastTickDuration,
		LastTickError:    p.lastTickErr,
	}
}

func (p *mirrorStatusPoller) tick() {
	dests := p.list()
	for i, rd := range dests {
		select {
		case <-p.stop:
			return
		default:
		}
		// Derive from p.ctx (not context.Background) so Stop's
		// cancel propagates into the in-flight check; with the
		// background-rooted ctx, a stuck ls-remote pinned Stop for
		// up to mirrorStatusPollPerCheckTimeout per destination.
		ctx, cancel := context.WithTimeout(p.ctx, mirrorStatusPollPerCheckTimeout)
		p.check(ctx, rd.Repo, rd.Dest.ID)
		cancel()
		// Stagger between checks. Skipped on the final iteration so
		// the loop exits without an extra sleep — the comment was
		// load-bearing but the code didn't honour it before the
		// poller test caught it. The select form lets Stop break
		// out of the stagger immediately rather than waiting the
		// full 250ms.
		if i < len(dests)-1 {
			select {
			case <-time.After(mirrorStatusPollStagger):
			case <-p.stop:
				return
			}
		}
	}
}

// Stop signals the poller goroutine to exit and waits for it.
// Idempotent. Cancels the poller-lifetime context so any in-flight
// check (typically `git ls-remote` shelling out via
// exec.CommandContext) is killed rather than running to its full
// per-check timeout — without that, PATCH /api/admin/mirror could
// hang for up to two minutes per stuck destination while waiting
// for Stop to return.
func (p *mirrorStatusPoller) Stop() {
	if p == nil {
		return
	}
	select {
	case <-p.stop:
		// Already closed.
	default:
		close(p.stop)
	}
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
}
