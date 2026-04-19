package auth

import (
	"encoding/json"
	"fmt"

	"github.com/petervdpas/GiGot/internal/crypto"
)

// SessionPersister persists active sessions to durable storage. Set via
// SessionStrategy.SetPersister. A nil persister means in-memory-only.
type SessionPersister interface {
	LoadSessions() ([]*Session, error)
	SaveSessions([]*Session) error
}

// SealedSessionStore persists *Session lists via a crypto.SealedFile.
// Implements SessionPersister.
type SealedSessionStore struct {
	file *crypto.SealedFile
}

// NewSealedSessionStore constructs a persister at the given file path.
// The parent directory is created with 0700 permissions if needed.
func NewSealedSessionStore(path string, enc *crypto.Encryptor) (*SealedSessionStore, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &SealedSessionStore{file: f}, nil
}

// LoadSessions reads and decrypts the session file. Missing file returns nil, nil.
func (s *SealedSessionStore) LoadSessions() ([]*Session, error) {
	plain, err := s.file.Load()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if plain == nil {
		return nil, nil
	}
	var entries []*Session
	if err := json.Unmarshal(plain, &entries); err != nil {
		return nil, fmt.Errorf("auth: parse session store: %w", err)
	}
	return entries, nil
}

// SaveSessions seals and atomically writes the session list.
func (s *SealedSessionStore) SaveSessions(entries []*Session) error {
	plain, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("auth: marshal sessions: %w", err)
	}
	return s.file.Save(plain)
}
