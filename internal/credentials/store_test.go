package credentials

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.enc")
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

func TestPut_Roundtrip(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.Put(Credential{Name: "github-personal", Kind: "pat", Secret: "ghp_abc"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("github-personal")
	if err != nil {
		t.Fatal(err)
	}
	if got.Secret != "ghp_abc" {
		t.Fatalf("secret = %q, want ghp_abc", got.Secret)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not stamped on create")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestPut_RequiresNameAndSecret(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Put(Credential{Kind: "pat", Secret: "x"}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := s.Put(Credential{Name: "x", Kind: "pat"}); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestPut_KindDefaultsToOther(t *testing.T) {
	s, _, _ := newTestStore(t)
	stored, err := s.Put(Credential{Name: "n", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Kind != "other" {
		t.Fatalf("kind = %q, want other", stored.Kind)
	}
}

func TestPut_RotationPreservesCreatedAtAndLastUsed(t *testing.T) {
	s, _, _ := newTestStore(t)
	first, _ := s.Put(Credential{Name: "github-personal", Kind: "pat", Secret: "old"})
	createdAt := first.CreatedAt

	// Record a use so LastUsed is non-nil before we rotate.
	if err := s.Touch("github-personal"); err != nil {
		t.Fatal(err)
	}
	used, _ := s.Get("github-personal")
	firstLastUsed := used.LastUsed
	if firstLastUsed == nil {
		t.Fatal("LastUsed not set by Touch")
	}

	// Sleep a smidge so any (wrong) re-stamp of CreatedAt would be visible.
	time.Sleep(2 * time.Millisecond)

	rotated, err := s.Put(Credential{Name: "github-personal", Kind: "pat", Secret: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Secret != "new" {
		t.Fatalf("secret = %q, want new", rotated.Secret)
	}
	if !rotated.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt changed on rotation: %v vs %v", rotated.CreatedAt, createdAt)
	}
	if rotated.LastUsed == nil || !rotated.LastUsed.Equal(*firstLastUsed) {
		t.Fatalf("LastUsed changed on rotation: %v vs %v", rotated.LastUsed, firstLastUsed)
	}
}

func TestPublicView_StripsSecret(t *testing.T) {
	c := Credential{Name: "n", Kind: "pat", Secret: "shh", Notes: "keep"}
	pv := c.PublicView()
	if pv.Secret != "" {
		t.Fatalf("PublicView should strip secret, got %q", pv.Secret)
	}
	if pv.Name != "n" || pv.Kind != "pat" || pv.Notes != "keep" {
		t.Fatal("PublicView should preserve metadata")
	}
	if c.Secret != "shh" {
		t.Fatal("PublicView mutated the original")
	}
}

func TestTouch_UpdatesLastUsed(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Put(Credential{Name: "n", Kind: "pat", Secret: "s"})
	before := time.Now().UTC()
	if err := s.Touch("n"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("n")
	if got.LastUsed == nil {
		t.Fatal("LastUsed nil after Touch")
	}
	if got.LastUsed.Before(before.Add(-time.Second)) {
		t.Fatalf("LastUsed looks stale: %v (before = %v)", got.LastUsed, before)
	}
}

func TestTouch_UnknownReturnsNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.Touch("missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMultipleCredentialsSameKind(t *testing.T) {
	// The feature the user explicitly called out: many PATs for the
	// same provider must coexist, keyed only by name.
	s, _, _ := newTestStore(t)
	names := []string{"github-personal", "github-work", "github-client-acme"}
	for _, n := range names {
		if _, err := s.Put(Credential{Name: n, Kind: "pat", Secret: "s-" + n}); err != nil {
			t.Fatalf("put %s: %v", n, err)
		}
	}
	if s.Count() != len(names) {
		t.Fatalf("count = %d, want %d", s.Count(), len(names))
	}
	for _, n := range names {
		got, err := s.Get(n)
		if err != nil {
			t.Fatalf("get %s: %v", n, err)
		}
		if got.Secret != "s-"+n {
			t.Fatalf("secret for %s = %q", n, got.Secret)
		}
	}
}

func TestAll_ReturnsSnapshot(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Put(Credential{Name: "a", Kind: "pat", Secret: "1"})
	_, _ = s.Put(Credential{Name: "b", Kind: "ssh", Secret: "2"})
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("all returned %d, want 2", len(all))
	}
}

func TestRemove(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Put(Credential{Name: "n", Kind: "pat", Secret: "s"})
	if err := s.Remove("n"); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatal("expected empty after Remove")
	}
	if err := s.Remove("n"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPersistence_SurvivesReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	_, _ = s.Put(Credential{Name: "github-personal", Kind: "pat", Secret: "ghp_abc"})

	s2, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("github-personal")
	if err != nil {
		t.Fatalf("missing after reopen: %v", err)
	}
	if got.Secret != "ghp_abc" {
		t.Fatalf("secret corrupted across restart: %q", got.Secret)
	}
}

func TestPersistence_DifferentServerCannotOpen(t *testing.T) {
	s, _, path := newTestStore(t)
	_, _ = s.Put(Credential{Name: "n", Kind: "pat", Secret: "s"})

	otherPriv, _, _ := crypto.GenerateKeyPair()
	otherEnc, _ := crypto.New(otherPriv)
	if _, err := Open(path, otherEnc); err == nil {
		t.Fatal("expected Open to fail for a different server's keypair")
	}
}
