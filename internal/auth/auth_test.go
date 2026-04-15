package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Strategy interface tests ---

func TestProviderNoStrategiesReturnsError(t *testing.T) {
	p := NewProvider()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := p.Authenticate(req)
	if err == nil {
		t.Fatal("expected error with no strategies")
	}
}

func TestProviderTriesStrategiesInOrder(t *testing.T) {
	p := NewProvider()

	// First strategy returns ErrNoCredentials (skip), second succeeds.
	p.Register(&stubStrategy{name: "skip", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "hit", identity: &Identity{Username: "alice", Provider: "hit"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	id, err := p.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Provider != "hit" {
		t.Errorf("expected provider 'hit', got %s", id.Provider)
	}
}

func TestProviderStopsOnHardError(t *testing.T) {
	p := NewProvider()

	p.Register(&stubStrategy{name: "fail", err: ErrInvalidToken})
	p.Register(&stubStrategy{name: "never", identity: &Identity{Username: "bob"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := p.Authenticate(req)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

// --- Middleware tests ---

func TestMiddlewareDisabledPassesThrough(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(false)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareEnabledRejectsUnauthenticated(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not have been called")
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareEnabledAllowsAuthenticated(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)

	ts := NewTokenStrategy()
	token, _ := ts.Issue("alice", nil)
	p.Register(ts)

	var gotIdentity *Identity
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if gotIdentity == nil {
		t.Fatal("expected identity in context")
	}
	if gotIdentity.Username != "alice" {
		t.Errorf("expected username alice, got %s", gotIdentity.Username)
	}
}

// --- IdentityFromContext tests ---

func TestIdentityFromContextReturnsNilWhenMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	id := IdentityFromContext(req.Context())
	if id != nil {
		t.Error("expected nil identity from empty context")
	}
}

// --- Stub strategy for testing ---

type stubStrategy struct {
	name     string
	identity *Identity
	err      error
}

func (s *stubStrategy) Name() string { return s.name }
func (s *stubStrategy) Authenticate(r *http.Request) (*Identity, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.identity, nil
}
