package server

import (
	"context"
	"log"
	"runtime/debug"
	"time"
)

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
}

func newMirrorStatusPoller(
	interval time.Duration,
	list func() []repoDestination,
	check func(ctx context.Context, repo, id string),
) *mirrorStatusPoller {
	p := &mirrorStatusPoller{
		interval: interval,
		list:     list,
		check:    check,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("mirror-status: poll tick panic: %v\n%s", r, debug.Stack())
		}
	}()
	p.tick()
}

func (p *mirrorStatusPoller) tick() {
	dests := p.list()
	for _, rd := range dests {
		select {
		case <-p.stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), mirrorStatusPollPerCheckTimeout)
		p.check(ctx, rd.Repo, rd.Dest.ID)
		cancel()
		// Stagger between checks. Skipped on the final iteration so
		// the loop exits without an extra sleep.
		time.Sleep(mirrorStatusPollStagger)
	}
}

// Stop signals the poller goroutine to exit and waits for it. Idempotent.
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
	<-p.done
}
