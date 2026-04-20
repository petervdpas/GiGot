package server

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// mirrorWorkerQueueSize is the buffered channel depth. Each slot is
// one (repo-name, trigger) coalesced event. 128 is deliberately
// modest — a burst bigger than this almost certainly means mirrors
// are backed up and the operator should investigate rather than have
// the in-memory queue grow without bound.
const mirrorWorkerQueueSize = 128

// mirrorPushPerDestTimeout caps the per-destination push the worker
// runs. Matches the hand-triggered mirrorPushTimeout from the manual
// endpoint — the same network round trip, the same cap.
const mirrorPushPerDestTimeout = mirrorPushTimeout

// mirrorWorker fans out a "some refs just moved on repo X" signal to
// every enabled destination attached to X. Buffered channel + single
// goroutine per process — deliberately not HA; queue entries are lost
// on restart (documented caveat in remote-sync.md §3.3 and the
// README roadmap). Retries on the push itself are out of scope for
// slice 2b; a failed push is recorded on the destination and the
// operator can re-trigger via the Sync-now button.
type mirrorWorker struct {
	queue chan string
	// fireOne does the per-destination push + record. Injected so the
	// worker is decoupled from *Server and unit tests can stub it.
	fireOne func(ctx context.Context, repo string, dest *destinations.Destination, cred *credentials.Credential) (*destinations.Destination, error)
	// listDests returns the current set of destinations for a repo.
	// Fetched per-event rather than snapshotted at enqueue so late
	// destination edits (enable/disable) take effect on the very next
	// push rather than the one after.
	listDests func(repo string) []*destinations.Destination
	// getCred resolves a credential name to its vault entry. Returns
	// credentials.ErrNotFound when the vault entry has been deleted
	// out from under a destination — the worker treats that as a
	// logged skip rather than a fatal error.
	getCred func(name string) (*credentials.Credential, error)
}

func newMirrorWorker(
	listDests func(repo string) []*destinations.Destination,
	getCred func(name string) (*credentials.Credential, error),
	fireOne func(ctx context.Context, repo string, dest *destinations.Destination, cred *credentials.Credential) (*destinations.Destination, error),
) *mirrorWorker {
	w := &mirrorWorker{
		queue:     make(chan string, mirrorWorkerQueueSize),
		listDests: listDests,
		getCred:   getCred,
		fireOne:   fireOne,
	}
	go w.run()
	return w
}

// enqueue schedules a fan-out for repo. Non-blocking: if the queue is
// full the event is dropped with a warning so a flaky remote doesn't
// stall the receive-pack handler that called us. The operator can
// always re-fire manually via the Sync-now button if a drop costs them
// a push.
func (w *mirrorWorker) enqueue(repo string) {
	select {
	case w.queue <- repo:
	default:
		log.Printf("mirror: queue full (depth %d); dropping fan-out for repo %q", mirrorWorkerQueueSize, repo)
	}
}

// run is the worker goroutine. Exits when the queue is closed.
func (w *mirrorWorker) run() {
	for repo := range w.queue {
		w.processRepo(repo)
	}
}

// processRepo fires one push per enabled destination on repo. Each
// push gets its own per-destination timeout context; a slow or stuck
// push on one destination therefore can't block the next one beyond
// the cap. Results are recorded on the destination (last_sync_*) and
// logged; nothing is surfaced back up to the client that originally
// pushed.
func (w *mirrorWorker) processRepo(repo string) {
	for _, d := range w.listDests(repo) {
		if !d.Enabled {
			continue
		}
		cred, err := w.getCred(d.CredentialName)
		if err != nil {
			if errors.Is(err, credentials.ErrNotFound) {
				log.Printf("mirror: repo %q destination %q references credential %q which is no longer in the vault; skipping", repo, d.ID, d.CredentialName)
				continue
			}
			log.Printf("mirror: repo %q destination %q credential lookup failed: %v", repo, d.ID, err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), mirrorPushPerDestTimeout)
		if _, err := w.fireOne(ctx, repo, d, cred); err != nil {
			// Only reached when the record itself couldn't be written.
			// A push failure at the remote is NOT an error here — it
			// lands in last_sync_error via fireOne's recording path.
			log.Printf("mirror: repo %q destination %q record update failed: %v", repo, d.ID, err)
		}
		cancel()
	}
}

// settleTimeout returns once the queue is drained and no handler is
// mid-flight, or errors if the deadline elapses first. Test-only —
// production code has no reason to block on the worker.
//
// Drain detection: spin until len(queue) == 0. We use a tiny sleep
// because the channel has no "drained" event. Good enough for tests
// that enqueue a finite, known number of events.
func (w *mirrorWorker) settle(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if len(w.queue) == 0 {
			// Give the worker a moment to finish the item it just
			// pulled off. In tests fireOne is synchronous so this is
			// a single scheduler yield.
			time.Sleep(5 * time.Millisecond)
			if len(w.queue) == 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("mirror worker did not settle before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
