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

// TokenStrategy authenticates requests via Bearer tokens.
type TokenStrategy struct {
	mu     sync.RWMutex
	tokens map[string]*TokenEntry // token string → entry
}

// NewTokenStrategy creates a new token-based strategy.
func NewTokenStrategy() *TokenStrategy {
	return &TokenStrategy{
		tokens: make(map[string]*TokenEntry),
	}
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
	s.tokens[token] = entry
	s.mu.Unlock()

	return token, nil
}

// Revoke removes a token.
func (s *TokenStrategy) Revoke(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.tokens[token]
	delete(s.tokens, token)
	return existed
}

// Load adds a pre-existing token entry (e.g. from config or persistence).
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

func generateToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
