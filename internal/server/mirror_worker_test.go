package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// mirrorWorkerTestServer builds a Server with auth disabled plus a
// baseline vault credential and one repo. Returns the server so tests
// can add destinations and swap pushDest as needed. The worker is
// already live — New() started its goroutine.
func mirrorWorkerTestServer(t *testing.T, repo string) *Server {
	t.Helper()
	srv := testServer(t)
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: "c", Kind: "pat", Secret: "s",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.git.InitBare(repo); err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestMirrorWorker_FiresOnlyEnabledDestinations is the core fan-out
// contract: given one enabled + one disabled destination on a repo,
// enqueueing that repo must fire the push for the enabled one and
// leave the disabled one alone. Without this, the enabled/disabled
// toggle means nothing for automatic sync.
func TestMirrorWorker_FiresOnlyEnabledDestinations(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-fan")

	enabled, err := srv.destinations.Add("repo-fan", destinations.Destination{
		URL: "https://enabled.example/x.git", CredentialName: "c", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.destinations.Add("repo-fan", destinations.Destination{
		URL: "https://disabled.example/x.git", CredentialName: "c", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var called []string
	srv.pushDest = func(_ context.Context, _, destURL, _ string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		called = append(called, destURL)
		return []byte("ok"), nil
	}

	srv.mirrorWorker.enqueue("repo-fan")
	if err := srv.mirrorWorker.settle(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(called) != 1 || called[0] != enabled.URL {
		t.Fatalf("want only enabled URL fired, got %v", called)
	}
}

// TestMirrorWorker_RecordsSuccess — a successful fan-out must leave
// last_sync_status=ok on the destination and touch the credential's
// LastUsed so the admin UI has the bookkeeping it needs without the
// operator having to hit Sync-now.
func TestMirrorWorker_RecordsSuccess(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-ok")
	dest, _ := srv.destinations.Add("repo-ok", destinations.Destination{
		URL: "https://x", CredentialName: "c", Enabled: true,
	})
	srv.pushDest = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return []byte("ok"), nil
	}

	srv.mirrorWorker.enqueue("repo-ok")
	if err := srv.mirrorWorker.settle(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	got, err := srv.destinations.Get("repo-ok", dest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncStatus != syncStatusOK {
		t.Fatalf("status: want %q, got %q", syncStatusOK, got.LastSyncStatus)
	}
	if got.LastSyncAt == nil {
		t.Fatal("last_sync_at should be set on success")
	}
	cred, _ := srv.credentials.Get("c")
	if cred.LastUsed == nil {
		t.Error("credentials.Touch should have fired on successful fan-out")
	}
}

// TestMirrorWorker_RecordsFailure — a failing push does NOT abort the
// queue; it lands as last_sync_status=error on the destination and
// the worker moves on. This is the "silent-and-log" failure mode from
// remote-sync.md §3.4 — the client push already succeeded at GiGot,
// so the mirror failure is observability, not a user-visible error.
func TestMirrorWorker_RecordsFailure(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-err")
	dest, _ := srv.destinations.Add("repo-err", destinations.Destination{
		URL: "https://x", CredentialName: "c", Enabled: true,
	})
	srv.pushDest = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return []byte("fatal: no such host"), errors.New("exit 128")
	}

	srv.mirrorWorker.enqueue("repo-err")
	if err := srv.mirrorWorker.settle(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	got, _ := srv.destinations.Get("repo-err", dest.ID)
	if got.LastSyncStatus != syncStatusError {
		t.Fatalf("status: want %q, got %q", syncStatusError, got.LastSyncStatus)
	}
	if got.LastSyncError == "" {
		t.Error("last_sync_error should carry the captured push output")
	}
	cred, _ := srv.credentials.Get("c")
	if cred.LastUsed != nil {
		t.Error("credentials.Touch must NOT fire on a failed push")
	}
}

// TestMirrorWorker_OneFailureDoesNotStopSiblings — with two enabled
// destinations where the first push fails, the second must still
// fire. Without this the first broken mirror would starve every
// other mirror on the same repo.
func TestMirrorWorker_OneFailureDoesNotStopSiblings(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-siblings")
	// Add a second credential so the two destinations don't collide
	// on touch-ordering assertions below.
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: "c2", Kind: "pat", Secret: "s2",
	}); err != nil {
		t.Fatal(err)
	}
	destA, _ := srv.destinations.Add("repo-siblings", destinations.Destination{
		URL: "https://a", CredentialName: "c", Enabled: true,
	})
	destB, _ := srv.destinations.Add("repo-siblings", destinations.Destination{
		URL: "https://b", CredentialName: "c2", Enabled: true,
	})
	srv.pushDest = func(_ context.Context, _, destURL, _ string) ([]byte, error) {
		if destURL == "https://a" {
			return []byte("boom"), errors.New("exit 1")
		}
		return []byte("ok"), nil
	}

	srv.mirrorWorker.enqueue("repo-siblings")
	if err := srv.mirrorWorker.settle(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	a, _ := srv.destinations.Get("repo-siblings", destA.ID)
	b, _ := srv.destinations.Get("repo-siblings", destB.ID)
	if a.LastSyncStatus != syncStatusError {
		t.Errorf("destA status: want %q, got %q", syncStatusError, a.LastSyncStatus)
	}
	if b.LastSyncStatus != syncStatusOK {
		t.Errorf("destB status: want %q (sibling must still fire), got %q", syncStatusOK, b.LastSyncStatus)
	}
}

// TestMirrorWorker_MissingCredentialSkipsAndContinues — a destination
// pointing at a credential that has been deleted out from under it
// (credential-vault deletion is normally blocked by Refs(), but a
// race or direct surgery can still produce this state) must be
// skipped with a log line, not crash the worker. The push for any
// other destination on the same repo must still run.
func TestMirrorWorker_MissingCredentialSkipsAndContinues(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-gone-cred")
	// Two destinations: one references the baseline credential "c",
	// one references a name that doesn't exist in the vault.
	destGood, _ := srv.destinations.Add("repo-gone-cred", destinations.Destination{
		URL: "https://good", CredentialName: "c", Enabled: true,
	})
	destOrphan, _ := srv.destinations.Add("repo-gone-cred", destinations.Destination{
		URL: "https://orphan", CredentialName: "gone", Enabled: true,
	})
	var mu sync.Mutex
	var calledURLs []string
	srv.pushDest = func(_ context.Context, _, destURL, _ string) ([]byte, error) {
		mu.Lock()
		calledURLs = append(calledURLs, destURL)
		mu.Unlock()
		return []byte("ok"), nil
	}

	srv.mirrorWorker.enqueue("repo-gone-cred")
	if err := srv.mirrorWorker.settle(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calledURLs) != 1 || calledURLs[0] != "https://good" {
		t.Fatalf("only the credentialled destination should have fired; got %v", calledURLs)
	}
	// The good destination should have a recorded ok status; the
	// orphan should be untouched (no last_sync_* write).
	good, _ := srv.destinations.Get("repo-gone-cred", destGood.ID)
	if good.LastSyncStatus != syncStatusOK {
		t.Errorf("good destination should be ok, got %q", good.LastSyncStatus)
	}
	orphan, _ := srv.destinations.Get("repo-gone-cred", destOrphan.ID)
	if orphan.LastSyncStatus != "" {
		t.Errorf("orphan destination should have no last_sync_status write; got %q", orphan.LastSyncStatus)
	}
}

// TestMirrorWorker_EnqueueIsNonBlockingWhenQueueFull — if the queue is
// saturated, further enqueues must drop+log rather than block the
// receive-pack handler (which is on the critical path of a user's
// `git push`). We simulate saturation by wedging the worker on a
// blocking pushDest, filling the queue, and asserting subsequent
// enqueues return immediately.
func TestMirrorWorker_EnqueueIsNonBlockingWhenQueueFull(t *testing.T) {
	srv := mirrorWorkerTestServer(t, "repo-block")
	if _, err := srv.destinations.Add("repo-block", destinations.Destination{
		URL: "https://x", CredentialName: "c", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Wedge the worker on the first event.
	release := make(chan struct{})
	var once sync.Once
	srv.pushDest = func(ctx context.Context, _, _, _ string) ([]byte, error) {
		once.Do(func() { <-release })
		return []byte("ok"), nil
	}
	srv.mirrorWorker.enqueue("repo-block") // worker pulls this, blocks inside pushDest

	// Fill the queue to capacity.
	for i := 0; i < mirrorWorkerQueueSize; i++ {
		srv.mirrorWorker.enqueue("repo-block")
	}

	// This one should be dropped, not block the caller. Measure with
	// a short deadline — if enqueue ever blocks, the test fails loud.
	done := make(chan struct{})
	go func() {
		srv.mirrorWorker.enqueue("repo-block")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("enqueue blocked on a full queue — the receive-pack handler would have hung too")
	}

	// Let the worker unblock so the goroutine doesn't leak past the test.
	close(release)
	_ = srv.mirrorWorker.settle(5 * time.Second)
}
