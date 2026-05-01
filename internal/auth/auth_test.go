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

// Browser navigations to a protected admin page used to render a bare
// "unauthorized" body — dead-end UX with no path back to sign-in. The
// middleware now bounces text/html GETs to the public landing page.
// API callers (no text/html in Accept) keep the original 401 so the
// admin SPA's guardSession() path is unchanged.
func TestMiddlewareHTMLNavigationRedirectsToRoot(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)

	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not have been called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/repositories", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("expected Location: /, got %q", loc)
	}
}

func TestMiddlewareAPICallStillGets401(t *testing.T) {
	p := NewProvider()
	p.SetEnabled(true)

	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not have been called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.Header.Set("Accept", "application/json, */*")
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
	token, _ := ts.Issue("alice", "repo-a", nil)
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
	token, _ := ts.Issue("alice", "repo-a", nil)
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
	token, _ := ts.Issue("alice", "repo-a", nil)
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
	token, _ := ts.Issue("alice", "repo-a", nil)
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

// --- Runtime mutation tests (Replace/Remove for hot-swap path) ---

// Replace on an existing strategy keeps the original position so the
// try-in-order contract isn't silently reordered when the admin UI
// swaps a provider's config out from under the middleware.
func TestProvider_ReplacePreservesOrder(t *testing.T) {
	p := NewProvider()
	p.Register(&stubStrategy{name: "a", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "b", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "c", identity: &Identity{Username: "c"}})

	newB := &stubStrategy{name: "b", identity: &Identity{Username: "new-b"}}
	if !p.Replace(newB) {
		t.Fatal("Replace returned false for existing strategy")
	}
	// Expected flow now: a returns no-creds (skip) → b hits → c never
	// runs. If Replace had appended instead, b would still return
	// no-creds and c would win with username "c".
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	id, err := p.Authenticate(req)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if id.Username != "new-b" {
		t.Errorf("expected new-b to win at position 2, got %q", id.Username)
	}
}

func TestProvider_ReplaceReturnsFalseOnMissing(t *testing.T) {
	p := NewProvider()
	p.Register(&stubStrategy{name: "a"})
	if p.Replace(&stubStrategy{name: "missing"}) {
		t.Error("Replace should return false when no matching strategy exists")
	}
	// And the original shouldn't have been clobbered.
	if len(p.strategies) != 1 || p.strategies[0].Name() != "a" {
		t.Errorf("original strategies list was mutated on miss: %+v", p.strategies)
	}
}

func TestProvider_Remove(t *testing.T) {
	p := NewProvider()
	p.Register(&stubStrategy{name: "a", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "gateway", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "c", identity: &Identity{Username: "c"}})

	if !p.Remove("gateway") {
		t.Fatal("Remove returned false for existing strategy")
	}
	if p.Remove("gateway") {
		t.Error("Remove should return false on the second call (already gone)")
	}
	if len(p.strategies) != 2 {
		t.Fatalf("strategies length = %d, want 2", len(p.strategies))
	}
	// Order of remaining entries must be preserved (a, c).
	if p.strategies[0].Name() != "a" || p.strategies[1].Name() != "c" {
		t.Errorf("remaining order wrong: %q, %q", p.strategies[0].Name(), p.strategies[1].Name())
	}
}

// Concurrent Authenticate readers alongside Replace writers must not
// race or see a torn strategies slice. -race flag catches the race;
// the assertion here is just "runs to completion without deadlock or
// panic." Critical because the hot-swap path is motivated by running
// under load.
func TestProvider_ReplaceConcurrentSafe(t *testing.T) {
	p := NewProvider()
	p.Register(&stubStrategy{name: "gateway", err: ErrNoCredentials})
	p.Register(&stubStrategy{name: "session", identity: &Identity{Username: "u"}})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_ = p.Replace(&stubStrategy{name: "gateway", err: ErrNoCredentials})
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if _, err := p.Authenticate(req); err != nil {
			t.Fatalf("auth during concurrent replace: %v", err)
		}
	}
	<-done
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
