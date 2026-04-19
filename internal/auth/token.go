package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// TokenEntry is a stored API token with its associated identity and the set
// of repositories the bearer is allowed to access. Repos is an allowlist: an
// empty slice grants access to no repositories. Wildcards are not supported
// — admins add repo names explicitly.
type TokenEntry struct {
	Token    string   `json:"token"`
	Username string   `json:"username"`
	Repos    []string `json:"repos,omitempty"`
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

// tokenFromRequest pulls the subscription token out of the request's
// Authorization header. Two schemes are accepted:
//
//   - Bearer <token>            — the documented modern form.
//   - Basic base64(user:token)  — the standard git-over-HTTP pattern
//     used by GitHub, GitLab, Gitea, etc. The Basic username is
//     cosmetic (ignored); the password is the subscription token.
//
// Supporting Basic lets `git clone http://user:<token>@host/git/repo`
// work out of the box, which is the usage the README documents.
// Returns "" for non-token requests (no header, unknown scheme, empty
// password) so callers can treat "" as "no credentials presented".
func tokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	switch {
	case strings.EqualFold(parts[0], "Bearer"):
		return strings.TrimSpace(parts[1])
	case strings.EqualFold(parts[0], "Basic"):
		if _, pass, ok := r.BasicAuth(); ok {
			return strings.TrimSpace(pass)
		}
	}
	return ""
}

// Authenticate checks the Authorization header for a valid token.
// See tokenFromRequest for the accepted schemes.
func (s *TokenStrategy) Authenticate(r *http.Request) (*Identity, error) {
	if r.Header.Get("Authorization") == "" {
		return nil, ErrNoCredentials
	}
	token := tokenFromRequest(r)
	if token == "" {
		// Header present but not a token scheme — let a later strategy
		// (session) take a shot.
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
		Provider: s.Name(),
	}, nil
}

// Issue creates and stores a new token for the given username, scoped to the
// given set of repositories. Pass nil or an empty slice to issue a token with
// no repo access (useful when the admin plans to attach repos later via an
// Update call).
func (s *TokenStrategy) Issue(username string, repos []string) (string, error) {
	token, err := generateToken(32)
	if err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}

	entry := &TokenEntry{
		Token:    token,
		Username: username,
		Repos:    repos,
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

// Get returns the TokenEntry for a bearer string, or nil if none exists. The
// returned pointer is shared; do not mutate it — use UpdateRepos instead.
func (s *TokenStrategy) Get(token string) *TokenEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens[token]
}

// EntryFromRequest extracts the token (Bearer or Basic password) from
// the Authorization header and returns the matching TokenEntry. Must
// accept both schemes — the policy layer uses this to pull the repo
// allowlist off the request, and Basic-auth callers (git) would
// otherwise reach the policy with a nil entry and get denied.
func (s *TokenStrategy) EntryFromRequest(r *http.Request) *TokenEntry {
	token := tokenFromRequest(r)
	if token == "" {
		return nil
	}
	return s.Get(token)
}

// UpdateRepos replaces the repo allowlist on an existing token. Returns
// ErrInvalidToken if the token doesn't exist.
func (s *TokenStrategy) UpdateRepos(token string, repos []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[token]
	if !ok {
		return ErrInvalidToken
	}
	previous := entry.Repos
	entry.Repos = repos
	if err := s.persistLocked(); err != nil {
		entry.Repos = previous
		return fmt.Errorf("persist token repos: %w", err)
	}
	return nil
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
