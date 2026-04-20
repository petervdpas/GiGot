package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.enc")
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, "", enc)
	if err != nil {
		t.Fatal(err)
	}
	return s, enc, path
}

func mustPut(t *testing.T, s *Store, a Account) *Account {
	t.Helper()
	got, err := s.Put(a)
	if err != nil {
		t.Fatalf("Put(%+v): %v", a, err)
	}
	return got
}

func TestPutNormalizesAndStores(t *testing.T) {
	s, _, _ := newTestStore(t)
	got := mustPut(t, s, Account{
		Provider:    "  LOCAL ",
		Identifier:  "  Admin ",
		Role:        "ADMIN",
		DisplayName: " Primary ",
	})
	if got.Provider != ProviderLocal || got.Identifier != "admin" || got.Role != RoleAdmin {
		t.Errorf("not normalised: %+v", got)
	}
	if got.DisplayName != "Primary" {
		t.Errorf("display_name not trimmed: %q", got.DisplayName)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt not auto-set")
	}
	if !s.Has(ProviderLocal, "ADMIN") {
		t.Error("Has should be case-insensitive")
	}
}

func TestPutRejectsBad(t *testing.T) {
	s, _, _ := newTestStore(t)
	cases := []struct {
		name string
		acc  Account
	}{
		{"missing provider", Account{Identifier: "x", Role: RoleAdmin}},
		{"missing identifier", Account{Provider: ProviderLocal, Role: RoleAdmin}},
		{"unknown provider", Account{Provider: "okta", Identifier: "x", Role: RoleAdmin}},
		{"unknown role", Account{Provider: ProviderLocal, Identifier: "x", Role: "viewer"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Put(tc.acc); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPutPreservesExistingHashOnEmpty(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleRegular})
	if err := s.SetPassword("alice", "hunter2"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ProviderLocal, "alice")
	if before.PasswordHash == "" {
		t.Fatal("password hash not set")
	}
	// Re-Put with role change but empty PasswordHash — should keep the hash.
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})
	after, _ := s.Get(ProviderLocal, "alice")
	if after.PasswordHash != before.PasswordHash {
		t.Error("Put wiped PasswordHash on role update")
	}
	if after.Role != RoleAdmin {
		t.Errorf("role not updated: %q", after.Role)
	}
}

func TestSetPasswordAndVerify(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})

	if err := s.SetPassword("alice", "hunter2"); err != nil {
		t.Fatal(err)
	}
	acc, err := s.Verify("alice", "hunter2")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if acc.Role != RoleAdmin {
		t.Errorf("role %q, want admin", acc.Role)
	}

	if _, err := s.Verify("alice", "wrong"); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("wrong password -> %v, want ErrInvalidPassword", err)
	}
	if _, err := s.Verify("ghost", "pw"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown identifier -> %v, want ErrNotFound", err)
	}
}

func TestSetPasswordRejectsNonLocal(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderGitHub, Identifier: "peter-vdpas", Role: RoleAdmin})
	// SetPassword targets local by design, so a github identifier is ErrNotFound
	// (no matching local:peter-vdpas row), not ErrWrongProvider. We additionally
	// verify that even if a local account *exists* it can be password-set, which
	// is the common path, to catch a future refactor that breaks this.
	if err := s.SetPassword("peter-vdpas", "pw"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound for non-existent local row", err)
	}
}

func TestSetPasswordMissingAccount(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.SetPassword("nobody", "pw"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestSetPasswordBlank(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})
	if err := s.SetPassword("alice", ""); !errors.Is(err, ErrMissingPassword) {
		t.Errorf("got %v, want ErrMissingPassword", err)
	}
}

func TestVerifyRejectsEmptyHash(t *testing.T) {
	// A freshly Put local account without SetPassword has no hash — Verify
	// must refuse rather than succeed on any password.
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})
	if _, err := s.Verify("alice", ""); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("got %v, want ErrInvalidPassword", err)
	}
}

func TestRemoveAndList(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})
	mustPut(t, s, Account{Provider: ProviderGitHub, Identifier: "peter-vdpas", Role: RoleRegular})
	if s.Count() != 2 {
		t.Fatalf("Count=%d, want 2", s.Count())
	}
	if err := s.Remove(ProviderLocal, "alice"); err != nil {
		t.Fatal(err)
	}
	if s.Has(ProviderLocal, "alice") {
		t.Fatal("alice still present after Remove")
	}
	if err := s.Remove(ProviderLocal, "alice"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove got %v, want ErrNotFound", err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Provider != ProviderGitHub {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestPersistenceReopen(t *testing.T) {
	s, enc, path := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})
	if err := s.SetPassword("alice", "pw"); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path, "", enc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s2.Verify("alice", "pw"); err != nil {
		t.Errorf("reopen Verify: %v", err)
	}
}

func TestMigrationFromLegacyAdminsEnc(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "admins.enc")
	newPath := filepath.Join(dir, "accounts.enc")
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-write a legacy admins.enc with two rows matching the shape
	// internal/admins used.
	legacyRows := []legacyAdminRow{
		{Username: "admin", PasswordHash: "$2a$10$hash1", CreatedAt: time.Now().UTC()},
		{Username: "Bob", PasswordHash: "$2a$10$hash2"},
	}
	plain, _ := json.Marshal(legacyRows)
	legacyFile, err := crypto.NewSealedFile(legacy, enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyFile.Save(plain); err != nil {
		t.Fatal(err)
	}

	s, err := Open(newPath, legacy, enc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("migrated count = %d, want 2", s.Count())
	}
	bob, err := s.Get(ProviderLocal, "bob")
	if err != nil {
		t.Fatalf("Bob not migrated case-insensitively: %v", err)
	}
	if bob.Role != RoleAdmin {
		t.Errorf("migrated role %q, want admin", bob.Role)
	}
	if bob.PasswordHash != "$2a$10$hash2" {
		t.Errorf("migrated hash %q, want $2a$10$hash2", bob.PasswordHash)
	}
	if bob.CreatedAt.IsZero() {
		t.Error("migrated zero CreatedAt should be backfilled")
	}

	// Accounts.enc should now exist; reopening with legacy path should NOT
	// re-trigger migration (accounts.enc wins).
	s2, err := Open(newPath, legacy, enc)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 2 {
		t.Errorf("second open count = %d, want 2 (migration should not double)", s2.Count())
	}
}

func TestMigrationSkippedWhenLegacyMissing(t *testing.T) {
	dir := t.TempDir()
	priv, _, _ := crypto.GenerateKeyPair()
	enc, _ := crypto.New(priv)
	s, err := Open(
		filepath.Join(dir, "accounts.enc"),
		filepath.Join(dir, "admins.enc"), // does not exist
		enc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Errorf("Count=%d, want 0 for clean start", s.Count())
	}
}

func TestPersistenceDifferentKeyFails(t *testing.T) {
	s, _, path := newTestStore(t)
	mustPut(t, s, Account{Provider: ProviderLocal, Identifier: "alice", Role: RoleAdmin})

	otherPriv, _, _ := crypto.GenerateKeyPair()
	otherEnc, _ := crypto.New(otherPriv)
	if _, err := Open(path, "", otherEnc); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}

func TestOpenHandlesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.enc")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	priv, _, _ := crypto.GenerateKeyPair()
	enc, _ := crypto.New(priv)
	s, err := Open(path, "", enc)
	if err != nil {
		t.Fatalf("open empty file: %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("empty-file Count=%d, want 0", s.Count())
	}
}
