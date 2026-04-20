// Package credentials stores secrets that GiGot uses on the admin's behalf
// when it talks to outside systems — GitHub / Azure DevOps / Gitea PATs,
// SSH keys, username+password pairs, and whatever shape comes next. The
// on-disk file is sealed to the server's own public key, same pattern as
// admins.enc / clients.enc / tokens.enc.
//
// Credentials are keyed by a user-chosen Name. Kind (pat, user_pass, ssh,
// other) is metadata — multiple credentials of the same Kind are allowed
// and expected (e.g. github-personal + github-work both kind=pat).
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

var (
	ErrNotFound = errors.New("credentials: not found")
)

// Credential is one named secret the server carries for outbound auth.
// Secret is stored verbatim; the handler layer is responsible for
// stripping it from responses via PublicView before it leaves the
// process (see docs/design/credential-vault.md §3).
type Credential struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Secret    string     `json:"secret"`
	Expires   *time.Time `json:"expires,omitempty"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Notes     string     `json:"notes,omitempty"`
}

// PublicView returns a copy of the credential with Secret zeroed. Use
// this anywhere the value crosses the admin API boundary — list
// responses, create echoes, anywhere a network consumer can see it.
func (c Credential) PublicView() Credential {
	c.Secret = ""
	return c
}

// Store holds credentials, persisted to an encrypted file on disk.
type Store struct {
	file *crypto.SealedFile

	mu    sync.RWMutex
	items map[string]*Credential
}

// Open loads (or initialises) the credential store at path, sealed to enc.
func Open(path string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	s := &Store{file: f, items: make(map[string]*Credential)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	if plain == nil {
		return nil
	}
	var list []*Credential
	if err := json.Unmarshal(plain, &list); err != nil {
		return fmt.Errorf("credentials: parse: %w", err)
	}
	for _, c := range list {
		s.items[c.Name] = c
	}
	return nil
}

func (s *Store) persist() error {
	list := make([]*Credential, 0, len(s.items))
	for _, c := range s.items {
		list = append(list, c)
	}
	plain, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// Put creates a new credential or rotates an existing one. Rotation
// preserves CreatedAt and LastUsed — the name identifies the
// long-lived credential, the Secret is just what it happens to be
// right now. Name and Secret are required; Kind defaults to "other"
// when empty so the store never holds untyped entries.
func (s *Store) Put(c Credential) (*Credential, error) {
	if c.Name == "" {
		return nil, fmt.Errorf("credentials: name required")
	}
	if c.Secret == "" {
		return nil, fmt.Errorf("credentials: secret required")
	}
	if c.Kind == "" {
		c.Kind = "other"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.items[c.Name]; ok {
		c.CreatedAt = existing.CreatedAt
		c.LastUsed = existing.LastUsed
	} else {
		c.CreatedAt = time.Now().UTC()
	}

	stored := c
	s.items[c.Name] = &stored
	if err := s.persist(); err != nil {
		delete(s.items, c.Name)
		return nil, err
	}
	// Return a copy so the caller's pointer doesn't alias the stored
	// struct — a subsequent Touch from another goroutine (mirror-sync
	// worker, future integrations) would otherwise race on the
	// caller's fields. Matches Get / All which already return snapshots.
	cp := stored
	return &cp, nil
}

// Get returns the full credential including the Secret. Callers that
// cross a network boundary MUST call PublicView on the result before
// returning it to the caller.
func (s *Store) Get(name string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[name]
	if !ok {
		return nil, ErrNotFound
	}
	copy := *c
	return &copy, nil
}

// All returns a snapshot of every credential. Same secret-safety
// contract as Get — strip via PublicView before serialising to the
// network.
func (s *Store) All() []*Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Credential, 0, len(s.items))
	for _, c := range s.items {
		copy := *c
		out = append(out, &copy)
	}
	return out
}

// Remove deletes a credential.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[name]; !ok {
		return ErrNotFound
	}
	delete(s.items, name)
	return s.persist()
}

// Touch records that a credential was just used, updating LastUsed to
// now. Callers (mirror-sync, future integrations) fire this on a
// successful outbound call so the admin UI can show "never used" vs
// "used 2 days ago" without extra bookkeeping.
func (s *Store) Touch(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[name]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	c.LastUsed = &now
	return s.persist()
}

// Count returns the number of stored credentials.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}
