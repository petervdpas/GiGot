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
func (s *SealedTokenStore) LoadTokens() ([]*TokenEntry, error) {
	plain, err := s.file.Load()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if plain == nil {
		return nil, nil
	}
	var entries []*TokenEntry
	if err := json.Unmarshal(plain, &entries); err != nil {
		return nil, fmt.Errorf("auth: parse token store: %w", err)
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
