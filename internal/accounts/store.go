// Package accounts is the directory of humans known to this GiGot
// server — admins and regulars side by side, keyed by (provider,
// identifier). Local-provider accounts carry a bcrypt password hash;
// other providers (github, entra, microsoft, gateway) rely on the IdP
// for proof and leave the hash empty. The on-disk file is NaCl-sealed
// to the server's own public key. See docs/design/accounts.md.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
	"golang.org/x/crypto/bcrypt"
)

// Role values. Three-tier model:
//
//   - admin       — full server control: accounts, repos, credential
//                   vault writes, subscription-key issuance.
//   - maintainer  — can hold and act on the "mirror" ability on their
//                   own subscription keys (manage destinations, fire
//                   manual syncs) and read credential names (no
//                   secrets) to reference vault entries when wiring
//                   mirrors. No account or repo administration.
//   - regular     — Formidable end user: push/pull templates and
//                   records via subscription keys. Cannot hold the
//                   mirror ability; the role is a structural fence on
//                   top of the per-token ability bits.
//
// No viewer / operator / per-repo roles. Per-repo scoping lives on
// subscription tokens (repos + abilities). The role decides which
// capability tiers an account is even allowed to hold.
const (
	RoleAdmin      = "admin"
	RoleMaintainer = "maintainer"
	RoleRegular    = "regular"
)

// Provider values. Additions should update docs/design/accounts.md §2.
const (
	ProviderLocal     = "local"
	ProviderGitHub    = "github"
	ProviderEntra     = "entra"
	ProviderMicrosoft = "microsoft"
	ProviderGateway   = "gateway"
)

var (
	KnownRoles     = []string{RoleAdmin, RoleMaintainer, RoleRegular}
	KnownProviders = []string{
		ProviderLocal, ProviderGitHub, ProviderEntra, ProviderMicrosoft, ProviderGateway,
	}
)

var (
	ErrNotFound        = errors.New("accounts: not found")
	ErrInvalidPassword = errors.New("accounts: invalid password")
	ErrBadProvider     = errors.New("accounts: unknown provider")
	ErrBadRole         = errors.New("accounts: unknown role")
	ErrWrongProvider   = errors.New("accounts: password operations are local-only")
	ErrMissingPassword = errors.New("accounts: password required")
)

// Account is one row in the directory. PasswordHash is populated only
// for Provider == ProviderLocal. CreatedAt is set on first Put when
// zero.
type Account struct {
	Provider     string    `json:"provider"`
	Identifier   string    `json:"identifier"`
	Role         string    `json:"role"`
	DisplayName  string    `json:"display_name,omitempty"`
	PasswordHash string    `json:"password_hash,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store holds accounts, persisted to a NaCl-sealed file on disk.
type Store struct {
	file *crypto.SealedFile

	mu    sync.RWMutex
	byKey map[string]*Account
}

// Open loads (or initialises) the accounts store at path, sealed to
// enc. If accounts.enc is missing AND legacyAdminsPath is non-empty
// AND that file exists, its rows are migrated as
// Account{Provider: local, Role: admin, PasswordHash: <existing>}.
// Pass an empty legacyAdminsPath to skip migration (tests, greenfield
// deploys).
func Open(path, legacyAdminsPath string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	s := &Store{file: f, byKey: make(map[string]*Account)}

	plain, err := f.Load()
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	if plain != nil {
		var list []*Account
		if err := json.Unmarshal(plain, &list); err != nil {
			return nil, fmt.Errorf("accounts: parse: %w", err)
		}
		for _, a := range list {
			s.byKey[key(a.Provider, a.Identifier)] = a
		}
		return s, nil
	}

	if legacyAdminsPath == "" {
		return s, nil
	}
	migrated, err := migrateLegacy(legacyAdminsPath, enc)
	if err != nil {
		return nil, fmt.Errorf("accounts: legacy migration: %w", err)
	}
	for _, a := range migrated {
		s.byKey[key(a.Provider, a.Identifier)] = a
	}
	if len(migrated) > 0 {
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// legacyAdminRow matches the on-disk shape of the old
// internal/admins/store.go Admin struct so migration can unmarshal it
// without importing that package (which will be deleted once migration
// is no longer needed).
type legacyAdminRow struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

func migrateLegacy(path string, enc *crypto.Encryptor) ([]*Account, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, err
	}
	plain, err := f.Load()
	if err != nil || plain == nil {
		return nil, err
	}
	var rows []legacyAdminRow
	if err := json.Unmarshal(plain, &rows); err != nil {
		return nil, err
	}
	out := make([]*Account, 0, len(rows))
	for _, r := range rows {
		id := strings.ToLower(strings.TrimSpace(r.Username))
		if id == "" {
			continue
		}
		created := r.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		out = append(out, &Account{
			Provider:     ProviderLocal,
			Identifier:   id,
			Role:         RoleAdmin,
			PasswordHash: r.PasswordHash,
			CreatedAt:    created,
		})
	}
	return out, nil
}

func key(provider, identifier string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" +
		strings.ToLower(strings.TrimSpace(identifier))
}

// Put upserts an Account. Normalises provider+identifier (lowercase,
// trim). Rejects unknown providers and roles. On an existing row, an
// empty incoming PasswordHash preserves the stored one — so the
// canonical way to set a password is SetPassword, not Put.
func (s *Store) Put(a Account) (*Account, error) {
	provider := strings.ToLower(strings.TrimSpace(a.Provider))
	identifier := strings.ToLower(strings.TrimSpace(a.Identifier))
	role := strings.ToLower(strings.TrimSpace(a.Role))
	if provider == "" || identifier == "" {
		return nil, fmt.Errorf("accounts: provider and identifier required")
	}
	if !slices.Contains(KnownProviders, provider) {
		return nil, fmt.Errorf("%w: %q", ErrBadProvider, a.Provider)
	}
	if !slices.Contains(KnownRoles, role) {
		return nil, fmt.Errorf("%w: %q", ErrBadRole, a.Role)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(provider, identifier)
	existing := s.byKey[k]
	acc := Account{
		Provider:     provider,
		Identifier:   identifier,
		Role:         role,
		DisplayName:  strings.TrimSpace(a.DisplayName),
		PasswordHash: a.PasswordHash,
		CreatedAt:    a.CreatedAt,
	}
	if existing != nil && acc.PasswordHash == "" {
		acc.PasswordHash = existing.PasswordHash
	}
	if existing != nil && acc.CreatedAt.IsZero() {
		acc.CreatedAt = existing.CreatedAt
	}
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}

	s.byKey[k] = &acc
	if err := s.persistLocked(); err != nil {
		if existing != nil {
			s.byKey[k] = existing
		} else {
			delete(s.byKey, k)
		}
		return nil, err
	}
	out := acc
	return &out, nil
}

// Get returns a copy of the account for (provider, identifier), or
// ErrNotFound.
func (s *Store) Get(provider, identifier string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.byKey[key(provider, identifier)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

// Has reports whether an account exists for (provider, identifier).
func (s *Store) Has(provider, identifier string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byKey[key(provider, identifier)]
	return ok
}

// SetPassword bcrypt-hashes password and stores it on the local
// account with identifier. Non-local accounts return ErrWrongProvider;
// missing accounts return ErrNotFound.
func (s *Store) SetPassword(identifier, password string) error {
	if password == "" {
		return ErrMissingPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("accounts: hash: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.byKey[key(ProviderLocal, identifier)]
	if !ok {
		return ErrNotFound
	}
	if existing.Provider != ProviderLocal {
		return ErrWrongProvider
	}
	prev := existing.PasswordHash
	existing.PasswordHash = string(hash)
	if err := s.persistLocked(); err != nil {
		existing.PasswordHash = prev
		return err
	}
	return nil
}

// Verify bcrypt-compares password against the stored hash on the local
// account with identifier. Runs a dummy compare on an unknown
// identifier so timing can't distinguish "wrong user" from "wrong
// password."
func (s *Store) Verify(identifier, password string) (*Account, error) {
	s.mu.RLock()
	a, ok := s.byKey[key(ProviderLocal, identifier)]
	s.mu.RUnlock()
	if !ok {
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$usesomesilentstringforleakproof."),
			[]byte(password),
		)
		return nil, ErrNotFound
	}
	if a.PasswordHash == "" {
		return nil, ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}
	cp := *a
	return &cp, nil
}

// Remove deletes the account for (provider, identifier).
func (s *Store) Remove(provider, identifier string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(provider, identifier)
	existing, ok := s.byKey[k]
	if !ok {
		return ErrNotFound
	}
	delete(s.byKey, k)
	if err := s.persistLocked(); err != nil {
		s.byKey[k] = existing
		return err
	}
	return nil
}

// List returns a copy of every account in the store.
func (s *Store) List() []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Account, 0, len(s.byKey))
	for _, a := range s.byKey {
		cp := *a
		out = append(out, &cp)
	}
	return out
}

// Count returns the total number of accounts.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byKey)
}

// persistLocked serialises the store. Must be called under s.mu.
func (s *Store) persistLocked() error {
	list := make([]*Account, 0, len(s.byKey))
	for _, a := range s.byKey {
		list = append(list, a)
	}
	plain, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("accounts: marshal: %w", err)
	}
	return s.file.Save(plain)
}
