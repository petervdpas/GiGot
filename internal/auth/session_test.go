package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionStrategyName(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	if s.Name() != "session" {
		t.Fatalf("got %q, want session", s.Name())
	}
}

func TestSessionCreateAndAuthenticate(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	sess, err := s.Create("local", "alice")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "alice" || id.Provider != "local" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestSessionAuthenticate_LegacyMissingProviderFallsBackToLocal(t *testing.T) {
	// Sessions minted before the Provider field existed were persisted
	// with an empty "provider" JSON field. They're still valid admin
	// sessions on the existing install (local-only era), so surface
	// them as provider="local" rather than breaking the user's day.
	s := NewSessionStrategy(time.Hour)
	sess, _ := s.Create("", "alice")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})

	id, err := s.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	if id.Provider != "local" {
		t.Fatalf("legacy session provider = %q, want local", id.Provider)
	}
}

func TestSessionAuthenticate_NoCookie(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	if _, err := s.Authenticate(req); err != ErrNoCredentials {
		t.Fatalf("got %v, want ErrNoCredentials", err)
	}
}

func TestSessionAuthenticate_BadCookie(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "nonsense"})
	if _, err := s.Authenticate(req); err != ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewSessionStrategy(10 * time.Millisecond)
	sess, _ := s.Create("local", "alice")

	time.Sleep(20 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	if _, err := s.Authenticate(req); err != ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken after expiry", err)
	}
}

func TestSessionDestroy(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	sess, _ := s.Create("local", "alice")
	if !s.Destroy(sess.ID) {
		t.Fatal("expected Destroy to return true")
	}
	if s.Destroy(sess.ID) {
		t.Fatal("expected second Destroy to return false")
	}
}

func TestSessionIDUniqueness(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		sess, err := s.Create("local", "u")
		if err != nil {
			t.Fatal(err)
		}
		if seen[sess.ID] {
			t.Fatalf("duplicate session id: %s", sess.ID)
		}
		seen[sess.ID] = true
	}
}
