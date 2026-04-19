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

// --- Basic auth path-scope tests ---
//
// These four scenarios lock in the "Basic only on whitelisted prefixes"
// defence in depth. They're deliberately arranged as two positive /
// negative pairs so both branches of the rule stay named and can't
// drift: Basic IS accepted on /git/* (positive), Basic IS rejected on
// everything else (negative); Bearer IS accepted on /api/* (positive),
// Bearer with a bad token IS rejected (negative via the invalid-token
// path).

func TestMiddlewareBasicOnWhitelistedPrefixAllowed(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)
	p.MarkBasicPrefix("/git/")

	ts := NewTokenStrategy()
	token, _ := ts.Issue("alice", nil)
	p.Register(ts)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/git/some-repo/info/refs", nil)
	req.SetBasicAuth("whoever", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler should run for Basic on /git/")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareBasicOutsideWhitelistRejected(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)
	p.MarkBasicPrefix("/git/")

	ts := NewTokenStrategy()
	token, _ := ts.Issue("alice", nil)
	p.Register(ts)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler must not run — Basic should be rejected outside /git/")
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.SetBasicAuth("whoever", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	// The 401 must advertise Bearer so a confused Basic-only client
	// can learn what scheme the API actually expects here.
	if got := rec.Header().Get("WWW-Authenticate"); !stringsHasPrefixFold(got, "Bearer") {
		t.Errorf("expected WWW-Authenticate: Bearer..., got %q", got)
	}
}

func TestMiddlewareBearerOnBearerOnlyPathAllowed(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)
	p.MarkBasicPrefix("/git/") // API path is NOT in the whitelist

	ts := NewTokenStrategy()
	token, _ := ts.Issue("alice", nil)
	p.Register(ts)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := p.Middleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler should run for Bearer on /api/repos")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareUnauthChallengeSchemePerPath(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)
	p.MarkBasicPrefix("/git/")
	p.Register(NewTokenStrategy()) // no tokens issued → always fails

	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not run when auth fails")
	}))

	// /git/* → challenge must be Basic (what git speaks).
	req := httptest.NewRequest(http.MethodGet, "/git/foo/info/refs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("WWW-Authenticate"); !stringsHasPrefixFold(got, "Basic") {
		t.Errorf("/git/ 401 should advertise Basic, got %q", got)
	}

	// /api/* → challenge must be Bearer (the documented scheme there).
	req = httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("WWW-Authenticate"); !stringsHasPrefixFold(got, "Bearer") {
		t.Errorf("/api/ 401 should advertise Bearer, got %q", got)
	}
}

// stringsHasPrefixFold is a minimal case-insensitive HasPrefix used by
// the challenge-scheme tests. Inline to avoid dragging strings.EqualFold
// plus slicing into every caller.
func stringsHasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a, b := s[i], prefix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
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
