package destinations

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "destinations.enc")
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	return s, enc, path
}

func TestAdd_AssignsIDAndStamps(t *testing.T) {
	s, _, _ := newTestStore(t)
	d, err := s.Add("addresses", Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == "" {
		t.Fatal("ID not assigned")
	}
	if d.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not stamped")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestAdd_RequiresFields(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Add("", Destination{URL: "u", CredentialName: "c"}); err == nil {
		t.Fatal("expected error for empty repo")
	}
	if _, err := s.Add("r", Destination{CredentialName: "c"}); err == nil {
		t.Fatal("expected error for empty url")
	}
	if _, err := s.Add("r", Destination{URL: "u"}); err == nil {
		t.Fatal("expected error for empty credential_name")
	}
}

func TestAdd_MultiplePerRepo_IDsAreDistinct(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, _ := s.Add("r", Destination{URL: "u1", CredentialName: "c"})
	b, _ := s.Add("r", Destination{URL: "u2", CredentialName: "c"})
	if a.ID == b.ID {
		t.Fatalf("IDs collided: %q", a.ID)
	}
	if len(s.All("r")) != 2 {
		t.Fatalf("All(r) = %d, want 2", len(s.All("r")))
	}
}

func TestAll_IsStableByCreatedAt(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, url := range []string{"u1", "u2", "u3"} {
		if _, err := s.Add("r", Destination{URL: url, CredentialName: "c"}); err != nil {
			t.Fatal(err)
		}
	}
	first := s.All("r")
	second := s.All("r")
	for i := range first {
		if first[i].URL != second[i].URL {
			t.Fatalf("All() order drifted: %v vs %v", first, second)
		}
	}
}

func TestGet_UnknownReturnsNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Get("r", "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	_, _ = s.Add("r", Destination{URL: "u", CredentialName: "c"})
	if _, err := s.Get("r", "still-wrong-id"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUpdate_MutatesInPlace_PreservesInvariants(t *testing.T) {
	s, _, _ := newTestStore(t)
	d, _ := s.Add("r", Destination{URL: "u", CredentialName: "c", Enabled: true})
	origID, origCreated := d.ID, d.CreatedAt

	updated, err := s.Update("r", d.ID, func(x *Destination) {
		x.URL = "u2"
		x.Enabled = false
		x.ID = "hacker-tried-to-change-id"
		x.CreatedAt = x.CreatedAt.Add(-10000)
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.URL != "u2" || updated.Enabled {
		t.Fatalf("mutation not applied: %+v", updated)
	}
	if updated.ID != origID {
		t.Fatalf("ID was rewritten: %q vs %q", updated.ID, origID)
	}
	if !updated.CreatedAt.Equal(origCreated) {
		t.Fatalf("CreatedAt was rewritten: %v vs %v", updated.CreatedAt, origCreated)
	}
}

func TestUpdate_UnknownReturnsNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Update("r", "nope", func(*Destination) {}); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRemove(t *testing.T) {
	s, _, _ := newTestStore(t)
	d, _ := s.Add("r", Destination{URL: "u", CredentialName: "c"})
	if err := s.Remove("r", d.ID); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatalf("count = %d, want 0", s.Count())
	}
	if err := s.Remove("r", d.ID); err != ErrNotFound {
		t.Fatalf("double-delete: want ErrNotFound, got %v", err)
	}
}

func TestRemoveAll_DropsEveryDestForRepo(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Add("r1", Destination{URL: "u1", CredentialName: "c"})
	_, _ = s.Add("r1", Destination{URL: "u2", CredentialName: "c"})
	_, _ = s.Add("r2", Destination{URL: "u3", CredentialName: "c"})

	if err := s.RemoveAll("r1"); err != nil {
		t.Fatal(err)
	}
	if len(s.All("r1")) != 0 {
		t.Fatal("r1 destinations not cleared")
	}
	if len(s.All("r2")) != 1 {
		t.Fatal("r2 destinations wrongly cleared")
	}
}

func TestRemoveAll_UnknownRepoIsNoop(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.RemoveAll("never-existed"); err != nil {
		t.Fatalf("want no error on unknown repo, got %v", err)
	}
}

func TestRefs_ReturnsUniqueRepoNames(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Add("addresses", Destination{URL: "u1", CredentialName: "github-personal"})
	_, _ = s.Add("addresses", Destination{URL: "u2", CredentialName: "github-personal"}) // same repo, same cred
	_, _ = s.Add("notes", Destination{URL: "u3", CredentialName: "github-personal"})
	_, _ = s.Add("notes", Destination{URL: "u4", CredentialName: "azdo-work"})

	refs := s.Refs("github-personal")
	if len(refs) != 2 {
		t.Fatalf("Refs = %v, want 2 unique repos", refs)
	}
	// Sorted alphabetically
	if refs[0] != "addresses" || refs[1] != "notes" {
		t.Fatalf("Refs = %v, want [addresses notes]", refs)
	}
	if len(s.Refs("unused-credential")) != 0 {
		t.Fatal("Refs should be empty for an unused credential")
	}
}

func TestPersistence_SurvivesReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	d, _ := s.Add("r", Destination{URL: "u", CredentialName: "c", Enabled: true})

	s2, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("r", d.ID)
	if err != nil {
		t.Fatalf("missing after reopen: %v", err)
	}
	if got.URL != "u" || got.CredentialName != "c" || !got.Enabled {
		t.Fatalf("corrupt across restart: %+v", got)
	}
}

func TestPersistence_DifferentServerCannotOpen(t *testing.T) {
	s, _, path := newTestStore(t)
	_, _ = s.Add("r", Destination{URL: "u", CredentialName: "c"})

	otherPriv, _, _ := crypto.GenerateKeyPair()
	otherEnc, _ := crypto.New(otherPriv)
	if _, err := Open(path, otherEnc); err == nil {
		t.Fatal("expected Open to fail for a different server's keypair")
	}
}

// TestAdd_ReturnsCopyNotAlias is the regression fence for the race the
// post-receive worker (internal/server/mirror_worker) hit under `go
// test -race`: Add used to return a pointer aliasing the stored
// struct, so a caller reading any field concurrently with a later
// Update would race. This test locks in "returned pointer is an
// independent snapshot" end-to-end: mutating the returned struct must
// not change the stored state.
func TestAdd_ReturnsCopyNotAlias(t *testing.T) {
	s, _, _ := newTestStore(t)
	got, err := s.Add("r", Destination{
		URL: "u", CredentialName: "c", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := got.ID

	// Mutate the returned value. If Add leaked the stored pointer,
	// this write would leak into the store.
	got.URL = "tampered-in-caller"
	got.Enabled = false

	fresh, err := s.Get("r", id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.URL != "u" {
		t.Errorf("caller mutation leaked into store: URL = %q, want %q", fresh.URL, "u")
	}
	if !fresh.Enabled {
		t.Error("caller mutation leaked into store: Enabled flipped to false")
	}
}

// addWithRemoteStatus is a test helper that adds a destination and
// then stamps every remote-status field on it so the tests below can
// observe what InvalidateRepoRemoteStatus clears.
func addWithRemoteStatus(t *testing.T, s *Store, repo string) string {
	t.Helper()
	d, err := s.Add(repo, Destination{URL: "u", CredentialName: "c", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = s.Update(repo, d.ID, func(x *Destination) {
		x.RemoteStatus = "in_sync"
		x.RemoteCheckedAt = &now
		x.RemoteCheckError = "stale-but-cleared"
		x.RemoteRefs = []RemoteRefStatus{
			{Ref: "refs/heads/main", Local: "abc", Remote: "abc", State: "same"},
		}
		past := now.Add(-time.Hour)
		x.LastSyncAt = &past
		x.LastSyncStatus = "ok"
		x.LastSyncError = ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestInvalidateRepoRemoteStatus_ClearsRemoteFields(t *testing.T) {
	s, _, _ := newTestStore(t)
	id := addWithRemoteStatus(t, s, "r")

	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("r", id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteStatus != "" {
		t.Errorf("RemoteStatus = %q, want empty", got.RemoteStatus)
	}
	if got.RemoteCheckedAt != nil {
		t.Errorf("RemoteCheckedAt = %v, want nil", got.RemoteCheckedAt)
	}
	if got.RemoteCheckError != "" {
		t.Errorf("RemoteCheckError = %q, want empty", got.RemoteCheckError)
	}
	if len(got.RemoteRefs) != 0 {
		t.Errorf("RemoteRefs = %+v, want empty", got.RemoteRefs)
	}
}

func TestInvalidateRepoRemoteStatus_LeavesLastSyncIntact(t *testing.T) {
	s, _, _ := newTestStore(t)
	id := addWithRemoteStatus(t, s, "r")

	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get("r", id)
	if got.LastSyncStatus != "ok" {
		t.Errorf("LastSyncStatus = %q, want ok", got.LastSyncStatus)
	}
	if got.LastSyncAt == nil {
		t.Error("LastSyncAt was nilled out — should survive remote-status invalidation")
	}
}

func TestInvalidateRepoRemoteStatus_LeavesOtherReposAlone(t *testing.T) {
	s, _, _ := newTestStore(t)
	_ = addWithRemoteStatus(t, s, "r1")
	otherID := addWithRemoteStatus(t, s, "r2")

	if err := s.InvalidateRepoRemoteStatus("r1"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get("r2", otherID)
	if got.RemoteStatus != "in_sync" {
		t.Errorf("r2 RemoteStatus was wrongly cleared: %q", got.RemoteStatus)
	}
	if got.RemoteCheckedAt == nil {
		t.Error("r2 RemoteCheckedAt was wrongly cleared")
	}
}

func TestInvalidateRepoRemoteStatus_UnknownRepoIsNoop(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.InvalidateRepoRemoteStatus("never-existed"); err != nil {
		t.Fatalf("want no error on unknown repo, got %v", err)
	}
}

func TestInvalidateRepoRemoteStatus_AllDestinationsOnRepoCleared(t *testing.T) {
	s, _, _ := newTestStore(t)
	idA := addWithRemoteStatus(t, s, "r")
	idB := addWithRemoteStatus(t, s, "r")
	idC := addWithRemoteStatus(t, s, "r")

	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{idA, idB, idC} {
		got, _ := s.Get("r", id)
		if got.RemoteStatus != "" || got.RemoteCheckedAt != nil || len(got.RemoteRefs) != 0 {
			t.Errorf("destination %s not fully cleared: %+v", id, got)
		}
	}
}

func TestInvalidateRepoRemoteStatus_IdempotentOnAlreadyEmpty(t *testing.T) {
	s, _, _ := newTestStore(t)
	d, err := s.Add("r", Destination{URL: "u", CredentialName: "c", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatalf("first invalidate on never-checked destination: %v", err)
	}
	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatalf("second invalidate (idempotent): %v", err)
	}
	got, _ := s.Get("r", d.ID)
	if got.RemoteStatus != "" || got.URL != "u" || got.CredentialName != "c" {
		t.Errorf("idempotent invalidate corrupted state: %+v", got)
	}
}

// TestInvalidateRepoRemoteStatus_ConcurrentSafeUnderRace exercises the
// write-lock: a burst of concurrent invalidations on the same repo
// must not race or leave the store in a torn state. Pair with
// `go test -race`.
func TestInvalidateRepoRemoteStatus_ConcurrentSafeUnderRace(t *testing.T) {
	s, _, _ := newTestStore(t)
	for i := 0; i < 5; i++ {
		_ = addWithRemoteStatus(t, s, "r")
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
				t.Errorf("concurrent invalidate: %v", err)
			}
		}()
	}
	wg.Wait()

	for _, d := range s.All("r") {
		if d.RemoteStatus != "" || d.RemoteCheckedAt != nil {
			t.Errorf("concurrent invalidate left tear: %+v", d)
		}
	}
}

func TestInvalidateRepoRemoteStatus_PersistsAcrossReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	id := addWithRemoteStatus(t, s, "r")

	if err := s.InvalidateRepoRemoteStatus("r"); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("r", id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteStatus != "" || got.RemoteCheckedAt != nil {
		t.Fatalf("invalidation not persisted: %+v", got)
	}
}
