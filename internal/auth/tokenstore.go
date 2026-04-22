package auth

import (
	"encoding/json"
	"fmt"

	"github.com/petervdpas/GiGot/internal/crypto"
)

// SealedTokenStore persists TokenEntry lists via a crypto.SealedFile.
// Implements TokenPersister.
type SealedTokenStore struct {
	file *crypto.SealedFile
}

// NewSealedTokenStore constructs a persister at the given file path. The
// parent directory is created with 0700 permissions if needed.
func NewSealedTokenStore(path string, enc *crypto.Encryptor) (*SealedTokenStore, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &SealedTokenStore{file: f}, nil
}

// LoadTokens reads and decrypts the token file. Missing file returns nil, nil.
//
// Subscription keys are now strictly one repo per key (§10 §
// design change). Pre-migration stores carry a "repos" list; this
// loader refuses to silently discard them and returns an error that
// names the affected token. Admins rebuild the store (re-issue keys,
// one per repo) and remove the old entries before starting the
// server again. No auto-split — we'd have to mint new token strings
// anyway, and every client would need a new key. Honest fail-closed
// beats quiet migration.
func (s *SealedTokenStore) LoadTokens() ([]*TokenEntry, error) {
	plain, err := s.file.Load()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if plain == nil {
		return nil, nil
	}

	// Dual-shape pre-parse: decode once into the new struct (which
	// just ignores unknown keys) and once into a probe that retains
	// the legacy "repos" list, so we can diagnose pre-migration
	// entries without contorting TokenEntry itself.
	var entries []*TokenEntry
	if err := json.Unmarshal(plain, &entries); err != nil {
		return nil, fmt.Errorf("auth: parse token store: %w", err)
	}
	type legacyProbe struct {
		Token string   `json:"token"`
		Repos []string `json:"repos"`
	}
	var probes []legacyProbe
	if err := json.Unmarshal(plain, &probes); err != nil {
		return nil, fmt.Errorf("auth: parse token store: %w", err)
	}
	for i, p := range probes {
		if len(p.Repos) > 0 || (i < len(entries) && entries[i].Repo == "") {
			return nil, fmt.Errorf("auth: token store contains pre-migration entry %q (multi-repo keys are no longer supported); revoke + re-issue one key per repo, then restart", p.Token)
		}
	}
	return entries, nil
}

// SaveTokens seals and atomically writes the token list.
func (s *SealedTokenStore) SaveTokens(entries []*TokenEntry) error {
	plain, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("auth: marshal tokens: %w", err)
	}
	return s.file.Save(plain)
}
