package clients

import (
	"path/filepath"
	"testing"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.enc")
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

func genClientKey(t *testing.T) string {
	t.Helper()
	_, pub, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return pub.Encode()
}

func TestEnroll_Roundtrip(t *testing.T) {
	s, _, _ := newTestStore(t)
	pk := genClientKey(t)

	c, err := s.Enroll("alice", pk)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "alice" || c.PublicKey != pk {
		t.Fatalf("unexpected client: %+v", c)
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestEnroll_IdempotentForSameKey(t *testing.T) {
	s, _, _ := newTestStore(t)
	pk := genClientKey(t)

	first, _ := s.Enroll("alice", pk)
	second, err := s.Enroll("alice", pk)
	if err != nil {
		t.Fatalf("re-enroll with same key should succeed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatal("same client should come back")
	}
}

func TestEnroll_RejectsDifferentKeyForSameID(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Enroll("alice", genClientKey(t))
	_, err := s.Enroll("alice", genClientKey(t))
	if err != ErrExists {
		t.Fatalf("expected ErrExists, got %v", err)
	}
}

func TestEnroll_RejectsBadKey(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.Enroll("bob", "not-base64!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestPersistence_SurvivesReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	pk := genClientKey(t)
	_, _ = s.Enroll("alice", pk)

	// Reopen with the same encryptor (simulates server restart).
	s2, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("alice")
	if err != nil {
		t.Fatalf("alice missing after reopen: %v", err)
	}
	if got.PublicKey != pk {
		t.Fatalf("pubkey corrupted across restart: %q vs %q", got.PublicKey, pk)
	}
}

func TestPersistence_DifferentServerCannotOpen(t *testing.T) {
	s, _, path := newTestStore(t)
	_, _ = s.Enroll("alice", genClientKey(t))

	// A different server tries to read the same file.
	otherPriv, _, _ := crypto.GenerateKeyPair()
	otherEnc, _ := crypto.New(otherPriv)
	if _, err := Open(path, otherEnc); err == nil {
		t.Fatal("expected Open to fail for a different server's keypair")
	}
}

func TestRemove(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Enroll("alice", genClientKey(t))
	if err := s.Remove("alice"); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatal("expected empty after Remove")
	}
	if err := s.Remove("alice"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPublicKey_ReturnsDecodedKey(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, pub, _ := crypto.GenerateKeyPair()
	_, _ = s.Enroll("alice", pub.Encode())

	got, err := s.PublicKey("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got != pub {
		t.Fatal("decoded pubkey differs from original")
	}
}
