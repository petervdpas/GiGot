// Package oauth is the Phase-3 redirect-flow login layer. See
// docs/design/accounts.md §8. One Provider per IdP, one in-memory
// StateStore for CSRF + PKCE, one Handler that ties the start +
// callback pair together.
//
// The package deliberately stays thin: no token refresh, no
// long-lived OAuth sessions, no per-provider user directory sync.
// GiGot's accounts store is still the source of truth; this layer
// only *authenticates* a human so the login handler can mint the
// same session cookie the local path already uses.
package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// StateEntry is one in-flight authorization-code exchange. Created
// when the user clicks a provider button, consumed on the first
// callback that matches by state value. CodeVerifier is the PKCE
// verifier we generated alongside the redirect; the IdP hashed its
// challenge, we hold onto the original so we can prove ownership at
// token-exchange time.
type StateEntry struct {
	Provider     string
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
	ReturnTo     string
}

// StateStore is the state+nonce ledger. Entries live for TTL; after
// that they're swept on the next write. Size is bounded implicitly
// by TTL — a hostile script slamming /admin/login can create at most
// one entry per request, and the cookie-size state param makes
// duplicate-state replay impossible without seeing the original.
type StateStore struct {
	mu      sync.Mutex
	entries map[string]*StateEntry
	ttl     time.Duration
	now     func() time.Time
}

// NewStateStore creates a store with the given TTL. A zero or
// negative TTL is treated as 10 minutes — long enough for a human to
// complete a multi-step IdP login, short enough that a captured
// state value stops being useful before anyone can act on it.
func NewStateStore(ttl time.Duration) *StateStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &StateStore{
		entries: make(map[string]*StateEntry),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Put records a new in-flight state and returns the opaque state
// value the caller should send to the IdP (and the caller should
// already have the nonce + code_verifier because the same values
// need to go into the authorize URL).
func (s *StateStore) Put(entry StateEntry) (string, error) {
	state, err := randomToken()
	if err != nil {
		return "", err
	}
	entry.CreatedAt = s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	s.entries[state] = &entry
	return state, nil
}

// Take pops the entry for state, removing it so it can't be
// replayed. Returns ErrStateNotFound when unknown or expired.
func (s *StateStore) Take(state string) (*StateEntry, error) {
	if state == "" {
		return nil, ErrStateNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	entry, ok := s.entries[state]
	if !ok {
		return nil, ErrStateNotFound
	}
	delete(s.entries, state)
	if s.now().Sub(entry.CreatedAt) > s.ttl {
		return nil, ErrStateExpired
	}
	return entry, nil
}

// sweepLocked drops everything older than TTL. Called on every write
// so the store can't grow unboundedly on a server with no callbacks.
// Must hold s.mu.
func (s *StateStore) sweepLocked() {
	cutoff := s.now().Add(-s.ttl)
	for k, e := range s.entries {
		if e.CreatedAt.Before(cutoff) {
			delete(s.entries, k)
		}
	}
}

// Len returns the current number of live entries (tests only).
func (s *StateStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// ErrStateNotFound signals a callback whose state value isn't in the
// store — either forged, already consumed, or evicted by TTL. Always
// surfaces to the user as the same opaque "login failed" so an
// attacker can't distinguish "wrong state" from "expired state".
var (
	ErrStateNotFound = errors.New("oauth: state not found")
	ErrStateExpired  = errors.New("oauth: state expired")
)

// randomToken returns a URL-safe 32-byte random identifier. Used for
// state, nonce, and PKCE code verifier — all three need to be
// unguessable and unique per request.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
