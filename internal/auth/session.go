package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SessionCookieName is the cookie name used for admin sessions.
const SessionCookieName = "gigot_session"

// Session represents a logged-in admin session. Exported JSON fields
// let the sealed persister round-trip these without a shadow DTO.
type Session struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionStrategy authenticates requests via a session cookie. Sessions
// live in memory by default; attach a SessionPersister via SetPersister
// to make them survive a restart.
type SessionStrategy struct {
	ttl time.Duration

	mu        sync.RWMutex
	sessions  map[string]*Session
	persister SessionPersister
}

// NewSessionStrategy creates a new session strategy with the given TTL.
func NewSessionStrategy(ttl time.Duration) *SessionStrategy {
	return &SessionStrategy{
		ttl:      ttl,
		sessions: make(map[string]*Session),
	}
}

// SetPersister attaches a persister and loads any non-expired sessions
// from it. Expired entries are dropped on load so the in-memory set
// never resurrects a dead session. Subsequent Create/Destroy calls
// write through to the persister.
func (s *SessionStrategy) SetPersister(p SessionPersister) error {
	if p == nil {
		return fmt.Errorf("auth: session persister must not be nil")
	}
	entries, err := p.LoadSessions()
	if err != nil {
		return fmt.Errorf("auth: load sessions: %w", err)
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persister = p
	for _, e := range entries {
		if e == nil || now.After(e.ExpiresAt) {
			continue
		}
		s.sessions[e.ID] = e
	}
	// Rewrite the file without the expired entries so a pathological
	// restart-loop can't let stale sessions accumulate on disk.
	return s.persistLocked()
}

// persistLocked writes the current session set through to the persister.
// Caller must hold s.mu. Nil persister is a no-op so in-memory-only mode
// stays cheap.
func (s *SessionStrategy) persistLocked() error {
	if s.persister == nil {
		return nil
	}
	entries := make([]*Session, 0, len(s.sessions))
	for _, e := range s.sessions {
		entries = append(entries, e)
	}
	return s.persister.SaveSessions(entries)
}

// Name returns "session".
func (s *SessionStrategy) Name() string { return "session" }

// Authenticate inspects the session cookie and returns the associated Identity.
func (s *SessionStrategy) Authenticate(r *http.Request) (*Identity, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, ErrNoCredentials
	}
	if c.Value == "" {
		return nil, ErrNoCredentials
	}

	s.mu.RLock()
	sess, ok := s.sessions[c.Value]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrInvalidToken
	}
	if time.Now().After(sess.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		// Best-effort persist — if it fails the stale entry simply stays
		// on disk until the next Create/Destroy/SetPersister sweep.
		_ = s.persistLocked()
		s.mu.Unlock()
		return nil, ErrInvalidToken
	}

	// Provider on the Identity is the upstream account provider (local,
	// microsoft, github, …) so handlers can look the account back up
	// and re-check its role on every request. A legacy session minted
	// before this field existed falls back to "local" — which was the
	// only login path at the time and matches the on-disk invariant.
	prov := sess.Provider
	if prov == "" {
		prov = "local"
	}
	return &Identity{
		ID:       sess.Username,
		Username: sess.Username,
		Provider: prov,
	}, nil
}

// Create mints a new session for the given (provider, username) and
// returns it. Provider is the originating account provider so admin
// role checks can walk back to the account record on every request.
func (s *SessionStrategy) Create(provider, username string) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}
	sess := &Session{
		ID:        id,
		Provider:  provider,
		Username:  username,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = sess
	if err := s.persistLocked(); err != nil {
		delete(s.sessions, id)
		return nil, fmt.Errorf("auth: persist session: %w", err)
	}
	return sess, nil
}

// Destroy removes a session by ID. Returns true if something was
// removed. A persister failure rolls the in-memory state back so the
// file and the map never drift.
func (s *SessionStrategy) Destroy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.sessions[id]
	if !ok {
		return false
	}
	delete(s.sessions, id)
	if err := s.persistLocked(); err != nil {
		s.sessions[id] = existing
		return false
	}
	return true
}

// Count returns the number of active sessions.
func (s *SessionStrategy) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Opportunistically prune expired sessions.
	n := 0
	now := time.Now()
	for _, sess := range s.sessions {
		if now.Before(sess.ExpiresAt) {
			n++
		}
	}
	return n
}

// ErrSessionCookieMissing is returned when a handler expects a session cookie
// but the request does not carry one.
var ErrSessionCookieMissing = errors.New("auth: session cookie missing")

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
