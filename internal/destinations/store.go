// Package destinations stores per-repo mirror-sync destinations — each
// one a URL plus the name of a credential-vault entry to authenticate
// with. The on-disk file is sealed to the server's own public key, same
// pattern as admins.enc / clients.enc / tokens.enc / credentials.enc.
//
// Destinations are keyed first by repo name, then by an opaque ID
// generated on Add. Storage only — the push worker that actually fires
// `git push` against each destination is a separate concern (see
// docs/design/remote-sync.md §3.3).
package destinations

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

var (
	ErrNotFound = errors.New("destinations: not found")
)

// Destination is one outbound mirror target for a repo.
type Destination struct {
	ID             string     `json:"id"`
	URL            string     `json:"url"`
	CredentialName string     `json:"credential_name"`
	Enabled        bool       `json:"enabled"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty"`
	LastSyncStatus string     `json:"last_sync_status,omitempty"`
	LastSyncError  string     `json:"last_sync_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Store holds destinations keyed by repo name, persisted to an encrypted
// file on disk.
type Store struct {
	file *crypto.SealedFile

	mu    sync.RWMutex
	items map[string]map[string]*Destination // repo → id → dest
}

// Open loads (or initialises) the destinations store at path, sealed to enc.
func Open(path string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("destinations: %w", err)
	}
	s := &Store{file: f, items: make(map[string]map[string]*Destination)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// persisted is the on-disk shape. A flat list keeps the file format
// trivially inspectable if it ever needs manual surgery.
type persisted struct {
	Repo        string       `json:"repo"`
	Destination *Destination `json:"destination"`
}

func (s *Store) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("destinations: %w", err)
	}
	if plain == nil {
		return nil
	}
	var list []persisted
	if err := json.Unmarshal(plain, &list); err != nil {
		return fmt.Errorf("destinations: parse: %w", err)
	}
	for _, p := range list {
		if _, ok := s.items[p.Repo]; !ok {
			s.items[p.Repo] = make(map[string]*Destination)
		}
		s.items[p.Repo][p.Destination.ID] = p.Destination
	}
	return nil
}

func (s *Store) persist() error {
	var list []persisted
	for repo, byID := range s.items {
		for _, d := range byID {
			list = append(list, persisted{Repo: repo, Destination: d})
		}
	}
	plain, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("destinations: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// newID returns a short URL-safe opaque ID. 12 random bytes = 16 chars
// after base64 URL encoding with padding stripped — enough entropy that
// a single repo will never see a collision.
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Add creates a new destination under the given repo. URL and
// CredentialName are required; Enabled defaults to true when the
// destination is meant to fire immediately. The caller is responsible
// for validating that the credential exists in the vault — this store
// does not cross-reference.
func (s *Store) Add(repo string, d Destination) (*Destination, error) {
	if repo == "" {
		return nil, fmt.Errorf("destinations: repo required")
	}
	if d.URL == "" {
		return nil, fmt.Errorf("destinations: url required")
	}
	if d.CredentialName == "" {
		return nil, fmt.Errorf("destinations: credential_name required")
	}

	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("destinations: id: %w", err)
	}
	d.ID = id
	d.CreatedAt = time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[repo]; !ok {
		s.items[repo] = make(map[string]*Destination)
	}
	stored := d
	s.items[repo][id] = &stored
	if err := s.persist(); err != nil {
		delete(s.items[repo], id)
		return nil, err
	}
	// Return a copy so the caller's pointer doesn't alias the stored
	// struct — subsequent Update calls from another goroutine would
	// otherwise race on the caller's fields. Matches Get / Update which
	// already return snapshots.
	cp := stored
	return &cp, nil
}

// Get returns one destination by repo + id.
func (s *Store) Get(repo, id string) (*Destination, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID, ok := s.items[repo]
	if !ok {
		return nil, ErrNotFound
	}
	d, ok := byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *d
	return &cp, nil
}

// All returns a snapshot of every destination on the given repo,
// ordered by CreatedAt so the list is stable across calls.
func (s *Store) All(repo string) []*Destination {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID, ok := s.items[repo]
	if !ok {
		return nil
	}
	out := make([]*Destination, 0, len(byID))
	for _, d := range byID {
		cp := *d
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Update applies a mutation callback to one destination under a write
// lock. Returns ErrNotFound if the destination doesn't exist. The
// callback must not retain the pointer past the call.
func (s *Store) Update(repo, id string, mutate func(*Destination)) (*Destination, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID, ok := s.items[repo]
	if !ok {
		return nil, ErrNotFound
	}
	d, ok := byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	before := *d
	mutate(d)
	// Preserve invariants the caller shouldn't be able to rewrite.
	d.ID = before.ID
	d.CreatedAt = before.CreatedAt
	if err := s.persist(); err != nil {
		*d = before
		return nil, err
	}
	cp := *d
	return &cp, nil
}

// Remove deletes a single destination.
func (s *Store) Remove(repo, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID, ok := s.items[repo]
	if !ok {
		return ErrNotFound
	}
	if _, ok := byID[id]; !ok {
		return ErrNotFound
	}
	delete(byID, id)
	if len(byID) == 0 {
		delete(s.items, repo)
	}
	return s.persist()
}

// RemoveAll drops every destination for a repo. Used when a repo
// itself is deleted so destinations don't dangle under a name that no
// longer exists.
func (s *Store) RemoveAll(repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[repo]; !ok {
		return nil
	}
	delete(s.items, repo)
	return s.persist()
}

// Refs returns the names of every repo that has at least one
// destination pointing at the given credential. Used to block credential
// deletion when removing it would orphan a live destination.
func (s *Store) Refs(credentialName string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	for repo, byID := range s.items {
		for _, d := range byID {
			if d.CredentialName == credentialName {
				seen[repo] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Count returns the total number of destinations across all repos.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, byID := range s.items {
		n += len(byID)
	}
	return n
}
