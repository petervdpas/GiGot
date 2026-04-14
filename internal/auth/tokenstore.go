package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/petervdpas/GiGot/internal/crypto"
)

// SealedTokenStore persists TokenEntry lists to a file, sealed to the given
// Encryptor's own public key. Implements TokenPersister.
type SealedTokenStore struct {
	path string
	enc  *crypto.Encryptor
}

// NewSealedTokenStore constructs a persister at the given file path. The
// parent directory is created with 0700 permissions if needed.
func NewSealedTokenStore(path string, enc *crypto.Encryptor) (*SealedTokenStore, error) {
	if path == "" {
		return nil, fmt.Errorf("auth: token store path required")
	}
	if enc == nil {
		return nil, fmt.Errorf("auth: token store encryptor required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("auth: mkdir token store: %w", err)
	}
	return &SealedTokenStore{path: path, enc: enc}, nil
}

// LoadTokens reads and decrypts the token file. Missing file returns nil, nil.
func (s *SealedTokenStore) LoadTokens() ([]*TokenEntry, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("auth: read token store: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	plain, err := s.enc.OpenSelf(data)
	if err != nil {
		return nil, fmt.Errorf("auth: decrypt token store: %w", err)
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
	sealed, err := s.enc.SealSelf(plain)
	if err != nil {
		return fmt.Errorf("auth: seal tokens: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0600); err != nil {
		return fmt.Errorf("auth: write token store: %w", err)
	}
	return os.Rename(tmp, s.path)
}
