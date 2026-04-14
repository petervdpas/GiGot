package admins

import (
	"path/filepath"
	"testing"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "admins.enc")
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

func TestPutAndVerify(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Put("alice", "hunter2"); err != nil {
		t.Fatal(err)
	}
	a, err := s.Verify("alice", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if a.Username != "alice" {
		t.Fatalf("got %q, want alice", a.Username)
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Put("alice", "hunter2")
	if _, err := s.Verify("alice", "bad"); err != ErrInvalidPassword {
		t.Fatalf("got %v, want ErrInvalidPassword", err)
	}
}

func TestVerify_UnknownUser(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Verify("ghost", "pw"); err != ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestPersistence_SurvivesReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	_, _ = s.Put("alice", "hunter2")

	s2, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Verify("alice", "hunter2"); err != nil {
		t.Fatal(err)
	}
}

func TestPersistence_DifferentServerCannotOpen(t *testing.T) {
	s, _, path := newTestStore(t)
	_, _ = s.Put("alice", "pw")

	otherPriv, _, _ := crypto.GenerateKeyPair()
	otherEnc, _ := crypto.New(otherPriv)
	if _, err := Open(path, otherEnc); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}

func TestRemove(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Put("alice", "pw")
	if err := s.Remove("alice"); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatal("expected 0 after Remove")
	}
	if err := s.Remove("alice"); err != ErrNotFound {
		t.Fatal("expected ErrNotFound")
	}
}

func TestPut_RequiresFields(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Put("", "pw"); err == nil {
		t.Fatal("expected error for empty username")
	}
	if _, err := s.Put("alice", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
}
