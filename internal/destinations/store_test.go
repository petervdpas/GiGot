package destinations

import (
	"path/filepath"
	"testing"

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
