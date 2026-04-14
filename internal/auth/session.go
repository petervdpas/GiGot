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

// Session represents a logged-in admin session.
type Session struct {
	ID        string
	Username  string
	Roles     []string
	ExpiresAt time.Time
}

// SessionStrategy authenticates requests via a session cookie. Sessions are
// held in memory; if the server restarts, admins re-login.
type SessionStrategy struct {
	ttl time.Duration

	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStrategy creates a new session strategy with the given TTL.
func NewSessionStrategy(ttl time.Duration) *SessionStrategy {
	return &SessionStrategy{
		ttl:      ttl,
		sessions: make(map[string]*Session),
	}
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
		s.mu.Unlock()
		return nil, ErrInvalidToken
	}

	return &Identity{
		ID:       sess.Username,
		Username: sess.Username,
		Roles:    sess.Roles,
		Provider: s.Name(),
	}, nil
}

// Create mints a new session for the given username/roles and returns it.
func (s *SessionStrategy) Create(username string, roles []string) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}
	sess := &Session{
		ID:        id,
		Username:  username,
		Roles:     roles,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess, nil
}

// Destroy removes a session by ID. Returns true if something was removed.
func (s *SessionStrategy) Destroy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return false
	}
	delete(s.sessions, id)
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
