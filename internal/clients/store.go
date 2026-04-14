// Package clients stores enrolled Formidable clients and their NaCl public
// keys. The on-disk file is sealed to the server's own public key so only the
// running server can read it.
package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

var (
	ErrNotFound = errors.New("clients: client not found")
	ErrExists   = errors.New("clients: client already enrolled")
)

// Client is a single enrolled Formidable client.
type Client struct {
	ID         string    `json:"id"`
	PublicKey  string    `json:"public_key"` // base64-encoded 32-byte key
	EnrolledAt time.Time `json:"enrolled_at"`
}

// Store holds enrolled clients, persisted to an encrypted file on disk.
type Store struct {
	file *crypto.SealedFile

	mu      sync.RWMutex
	clients map[string]*Client
}

// Open loads the store from path (creating an empty one if the file is missing).
// The file is sealed to the server's own public key.
func Open(path string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("clients: %w", err)
	}
	s := &Store{file: f, clients: make(map[string]*Client)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("clients: %w", err)
	}
	if plain == nil {
		return nil
	}
	var list []*Client
	if err := json.Unmarshal(plain, &list); err != nil {
		return fmt.Errorf("clients: parse: %w", err)
	}
	for _, c := range list {
		s.clients[c.ID] = c
	}
	return nil
}

func (s *Store) persist() error {
	list := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		list = append(list, c)
	}
	plain, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("clients: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// Enroll records a new client. Returns ErrExists if the ID is already taken
// with a *different* public key. Re-enrolling with the same pubkey is a no-op.
func (s *Store) Enroll(id, publicKey string) (*Client, error) {
	if id == "" {
		return nil, fmt.Errorf("clients: id required")
	}
	if _, err := crypto.DecodeKey(publicKey); err != nil {
		return nil, fmt.Errorf("clients: public_key: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.clients[id]; ok {
		if existing.PublicKey == publicKey {
			return existing, nil
		}
		return nil, ErrExists
	}
	c := &Client{
		ID:         id,
		PublicKey:  publicKey,
		EnrolledAt: time.Now().UTC(),
	}
	s.clients[id] = c
	if err := s.persist(); err != nil {
		delete(s.clients, id)
		return nil, err
	}
	return c, nil
}

// Get returns the client with the given ID.
func (s *Store) Get(id string) (*Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

// PublicKey is a convenience that returns the decoded NaCl key for a client.
func (s *Store) PublicKey(id string) (crypto.Key, error) {
	c, err := s.Get(id)
	if err != nil {
		return crypto.Key{}, err
	}
	return crypto.DecodeKey(c.PublicKey)
}

// All returns a snapshot of all enrolled clients.
func (s *Store) All() []*Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c)
	}
	return out
}

// Remove deletes a client.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[id]; !ok {
		return ErrNotFound
	}
	delete(s.clients, id)
	return s.persist()
}

// Count returns the number of enrolled clients.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}
