package auth

import (
	"path/filepath"
	"testing"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestEncryptor(t *testing.T) *crypto.Encryptor {
	t.Helper()
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestSealedTokenStore_EmptyFileReturnsNil(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, err := NewSealedTokenStore(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries for missing file, got %d", len(entries))
	}
}

func TestSealedTokenStore_RoundTrip(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, _ := NewSealedTokenStore(path, enc)

	in := []*TokenEntry{
		{Token: "t1", Username: "alice", Repo: "r1"},
		{Token: "t2", Username: "bob", Repo: "r2"},
	}
	if err := s.SaveTokens(in); err != nil {
		t.Fatal(err)
	}

	out, err := s.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
}

func TestSealedTokenStore_RoundTripPreservesAbilities(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, _ := NewSealedTokenStore(path, enc)

	in := []*TokenEntry{
		{Token: "t1", Username: "alice", Repo: "r1", Abilities: []string{"mirror"}},
		{Token: "t2", Username: "bob", Repo: "r2"}, // no abilities
	}
	if err := s.SaveTokens(in); err != nil {
		t.Fatal(err)
	}

	out, err := s.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	byToken := map[string]*TokenEntry{}
	for _, e := range out {
		byToken[e.Token] = e
	}
	if got := byToken["t1"]; got == nil || len(got.Abilities) != 1 || got.Abilities[0] != "mirror" {
		t.Fatalf("t1 abilities not preserved: %+v", got)
	}
	if got := byToken["t2"]; got == nil || len(got.Abilities) != 0 {
		t.Fatalf("t2 should have no abilities, got %+v", got)
	}
}

func TestSealedTokenStore_RejectsWrongKey(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, _ := NewSealedTokenStore(path, enc)
	_ = s.SaveTokens([]*TokenEntry{{Token: "t1", Username: "alice", Repo: "r1"}})

	other := newTestEncryptor(t)
	s2, _ := NewSealedTokenStore(path, other)
	if _, err := s2.LoadTokens(); err == nil {
		t.Fatal("expected decryption to fail with a different key")
	}
}

func TestTokenStrategy_PersistsAcrossRestart(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")

	store1, _ := NewSealedTokenStore(path, enc)
	s1 := NewTokenStrategy()
	if err := s1.SetPersister(store1); err != nil {
		t.Fatal(err)
	}
	token, err := s1.Issue("alice", "repo-a", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate restart: fresh strategy + store pointing at the same file.
	store2, _ := NewSealedTokenStore(path, enc)
	s2 := NewTokenStrategy()
	if err := s2.SetPersister(store2); err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 1 {
		t.Fatalf("expected 1 token after reload, got %d", s2.Count())
	}
	if _, ok := s2.tokens[token]; !ok {
		t.Fatal("issued token not present after reload")
	}
}

func TestTokenStrategy_RevokeSurvivesRestart(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")

	store, _ := NewSealedTokenStore(path, enc)
	s := NewTokenStrategy()
	_ = s.SetPersister(store)
	token, _ := s.Issue("alice", "repo-a", nil)
	if !s.Revoke(token) {
		t.Fatal("revoke returned false")
	}

	store2, _ := NewSealedTokenStore(path, enc)
	s2 := NewTokenStrategy()
	_ = s2.SetPersister(store2)
	if s2.Count() != 0 {
		t.Fatalf("expected 0 tokens after revoke+reload, got %d", s2.Count())
	}
}

// TestSealedTokenStore_RejectsPreMigrationEntries is the load-bearing
// migration guard: a token store that still carries the legacy
// multi-repo "repos" list refuses to load, and the error names the
// affected token so the admin can find it. We never silently drop
// or split multi-repo keys — the client would lose access without
// warning.
func TestSealedTokenStore_RejectsPreMigrationEntries(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, _ := NewSealedTokenStore(path, enc)

	// Hand-craft the legacy JSON shape: a "repos" array and no "repo"
	// field. Write it sealed via the same file abstraction so the
	// loader sees a real encrypted blob, not a raw fixture.
	legacy := `[{"token":"legacy-t1","username":"alice","repos":["r1","r2"]}]`
	if err := s.file.Save([]byte(legacy)); err != nil {
		t.Fatal(err)
	}

	_, err := s.LoadTokens()
	if err == nil {
		t.Fatal("LoadTokens should refuse pre-migration entries")
	}
	if !containsSubstr(err.Error(), "legacy-t1") {
		t.Fatalf("error should name the offending token; got %q", err.Error())
	}
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexSubstr(s, sub) >= 0))
}
func indexSubstr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
