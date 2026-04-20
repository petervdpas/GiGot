package auth

import (
	"net/http/httptest"
	"testing"
)

func TestTokenStrategyName(t *testing.T) {
	s := NewTokenStrategy()
	if s.Name() != "token" {
		t.Errorf("expected name 'token', got %s", s.Name())
	}
}

func TestTokenIssueAndAuthenticate(t *testing.T) {
	s := NewTokenStrategy()
	token, err := s.Issue("alice", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Username != "alice" {
		t.Errorf("expected username alice, got %s", id.Username)
	}
	if id.Provider != "token" {
		t.Errorf("expected provider token, got %s", id.Provider)
	}
}

func TestTokenAuthenticateNoHeader(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)

	_, err := s.Authenticate(req)
	if err != ErrNoCredentials {
		t.Errorf("expected ErrNoCredentials, got %v", err)
	}
}

// TestTokenAuthenticateBasicWithValidToken proves the Basic-auth path
// works — this is the shape `git clone http://user:<token>@host/...`
// produces, so Basic support is load-bearing for the documented
// clone flow.
func TestTokenAuthenticateBasicWithValidToken(t *testing.T) {
	s := NewTokenStrategy()
	token, err := s.Issue("alice", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	// Git's default username from the URL is ignored — the password is
	// the credential. Use a non-matching username to prove that.
	req.SetBasicAuth("whatever", token)

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatalf("expected success on Basic auth with valid token, got %v", err)
	}
	if id.Username != "alice" {
		t.Errorf("identity username from token entry, not URL: got %q, want %q", id.Username, "alice")
	}
}

// TestTokenAuthenticateBasicWithInvalidToken locks in: a Basic auth
// attempt with a non-existent token is a hard reject (ErrInvalidToken),
// not a silent pass-through to the next strategy.
func TestTokenAuthenticateBasicWithInvalidToken(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "totally-not-a-real-token")

	_, err := s.Authenticate(req)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for bogus Basic password, got %v", err)
	}
}

// TestTokenAuthenticateBasicWithEmptyPassword degrades to
// ErrNoCredentials so a subsequent strategy gets a chance — a Basic
// header with no password is equivalent to "no credentials provided".
func TestTokenAuthenticateBasicWithEmptyPassword(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "")

	_, err := s.Authenticate(req)
	if err != ErrNoCredentials {
		t.Errorf("expected ErrNoCredentials for empty Basic password, got %v", err)
	}
}

// TestTokenAuthenticateUnknownScheme guards the fallthrough: schemes
// other than Bearer / Basic must return ErrNoCredentials so the next
// strategy in the provider chain gets tried.
func TestTokenAuthenticateUnknownScheme(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Digest nonsense")

	_, err := s.Authenticate(req)
	if err != ErrNoCredentials {
		t.Errorf("expected ErrNoCredentials for unknown scheme, got %v", err)
	}
}

func TestTokenAuthenticateInvalidToken(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer totally-bogus-token")

	_, err := s.Authenticate(req)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestTokenAuthenticateCaseInsensitiveBearer(t *testing.T) {
	s := NewTokenStrategy()
	token, _ := s.Issue("bob", nil, nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "bearer "+token)

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Username != "bob" {
		t.Errorf("expected username bob, got %s", id.Username)
	}
}

func TestTokenRevoke(t *testing.T) {
	s := NewTokenStrategy()
	token, _ := s.Issue("alice", nil, nil)

	if !s.Revoke(token) {
		t.Error("expected revoke to return true for existing token")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := s.Authenticate(req)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken after revoke, got %v", err)
	}
}

func TestTokenRevokeNonExistent(t *testing.T) {
	s := NewTokenStrategy()
	if s.Revoke("nonexistent") {
		t.Error("expected revoke to return false for nonexistent token")
	}
}

func TestTokenLoad(t *testing.T) {
	s := NewTokenStrategy()
	s.Load(&TokenEntry{
		Token:    "preloaded-token-123",
		Username: "service-account",
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer preloaded-token-123")

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Username != "service-account" {
		t.Errorf("expected username service-account, got %s", id.Username)
	}
}

func TestTokenCount(t *testing.T) {
	s := NewTokenStrategy()
	if s.Count() != 0 {
		t.Errorf("expected 0, got %d", s.Count())
	}

	s.Issue("a", nil, nil)
	s.Issue("b", nil, nil)
	if s.Count() != 2 {
		t.Errorf("expected 2, got %d", s.Count())
	}
}

func TestTokenUniqueness(t *testing.T) {
	s := NewTokenStrategy()
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := s.Issue("user", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tokens[token] {
			t.Fatalf("duplicate token generated: %s", token)
		}
		tokens[token] = true
	}
}

func TestTokenEmptyBearerValue(t *testing.T) {
	s := NewTokenStrategy()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ")

	_, err := s.Authenticate(req)
	if err != ErrNoCredentials {
		t.Errorf("expected ErrNoCredentials for empty bearer, got %v", err)
	}
}
