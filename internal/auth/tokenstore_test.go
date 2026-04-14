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
		{Token: "t1", Username: "alice", Roles: []string{"admin"}},
		{Token: "t2", Username: "bob", Roles: []string{"reader"}},
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

func TestSealedTokenStore_RejectsWrongKey(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s, _ := NewSealedTokenStore(path, enc)
	_ = s.SaveTokens([]*TokenEntry{{Token: "t1", Username: "alice"}})

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
	token, err := s1.Issue("alice", []string{"admin"})
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
	token, _ := s.Issue("alice", nil)
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
