package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// TokenEntry is a stored API token with its associated identity.
type TokenEntry struct {
	Token    string   `json:"token"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// TokenPersister persists the token set to durable storage. Set via
// TokenStrategy.SetPersister. nil is allowed for in-memory-only use.
type TokenPersister interface {
	LoadTokens() ([]*TokenEntry, error)
	SaveTokens([]*TokenEntry) error
}

// TokenStrategy authenticates requests via Bearer tokens.
type TokenStrategy struct {
	mu        sync.RWMutex
	tokens    map[string]*TokenEntry // token string → entry
	persister TokenPersister
}

// NewTokenStrategy creates a new token-based strategy.
func NewTokenStrategy() *TokenStrategy {
	return &TokenStrategy{
		tokens: make(map[string]*TokenEntry),
	}
}

// SetPersister attaches a persister and loads any existing tokens from it.
// Subsequent Issue/Revoke/Load calls will write through to the persister.
func (s *TokenStrategy) SetPersister(p TokenPersister) error {
	if p == nil {
		return fmt.Errorf("auth: persister must not be nil")
	}
	entries, err := p.LoadTokens()
	if err != nil {
		return fmt.Errorf("auth: load tokens: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persister = p
	for _, e := range entries {
		s.tokens[e.Token] = e
	}
	return nil
}

// persistLocked writes the current token set through to the persister.
// Caller must hold s.mu. Errors are returned so Issue/Revoke can surface them.
func (s *TokenStrategy) persistLocked() error {
	if s.persister == nil {
		return nil
	}
	entries := make([]*TokenEntry, 0, len(s.tokens))
	for _, e := range s.tokens {
		entries = append(entries, e)
	}
	return s.persister.SaveTokens(entries)
}

// Name returns "token".
func (s *TokenStrategy) Name() string {
	return "token"
}

// Authenticate checks the Authorization header for a valid Bearer token.
func (s *TokenStrategy) Authenticate(r *http.Request) (*Identity, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return nil, ErrNoCredentials
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, ErrNoCredentials
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return nil, ErrNoCredentials
	}

	s.mu.RLock()
	entry, ok := s.tokens[token]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrInvalidToken
	}

	return &Identity{
		ID:       entry.Username,
		Username: entry.Username,
		Roles:    entry.Roles,
		Provider: s.Name(),
	}, nil
}

// Issue creates and stores a new token for the given username and roles.
func (s *TokenStrategy) Issue(username string, roles []string) (string, error) {
	token, err := generateToken(32)
	if err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}

	entry := &TokenEntry{
		Token:    token,
		Username: username,
		Roles:    roles,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = entry
	if err := s.persistLocked(); err != nil {
		delete(s.tokens, token)
		return "", fmt.Errorf("persisting token: %w", err)
	}

	return token, nil
}

// Revoke removes a token. Returns true if a token was removed.
func (s *TokenStrategy) Revoke(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, existed := s.tokens[token]
	if !existed {
		return false
	}
	delete(s.tokens, token)
	if err := s.persistLocked(); err != nil {
		// Roll back — caller shouldn't see partial state.
		s.tokens[token] = existing
		return false
	}
	return true
}

// Load adds a pre-existing token entry (e.g. from config). Does not persist;
// use this for bootstrap-only entries.
func (s *TokenStrategy) Load(entry *TokenEntry) {
	s.mu.Lock()
	s.tokens[entry.Token] = entry
	s.mu.Unlock()
}

// Count returns the number of active tokens.
func (s *TokenStrategy) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}

// List returns a snapshot of all token entries.
func (s *TokenStrategy) List() []*TokenEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*TokenEntry, 0, len(s.tokens))
	for _, e := range s.tokens {
		out = append(out, e)
	}
	return out
}

func generateToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
