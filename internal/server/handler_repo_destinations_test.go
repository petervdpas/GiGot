package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// subscriberTestServer spins up a fresh server with auth enabled, one
// credential in the vault, one bare repo named "addresses", and a
// local maintainer account "alice" so subscriber tokens issued for
// "alice" pass both the runtime role gate (maintainer can hold mirror)
// and the issue-time ability check. Returns the server and the repo
// name so tests can issue tokens with whatever mix of scopes/abilities
// they need.
func subscriberTestServer(t *testing.T) *Server {
	t.Helper()
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: "github-personal", Kind: "pat", Secret: "ghp_x",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "alice",
		Role:       accounts.RoleMaintainer,
	}); err != nil {
		t.Fatal(err)
	}
	return srv
}

// bearer builds a request with a Bearer token attached.
func bearer(t *testing.T, method, path string, body any, token string) *http.Request {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// TestRepoDestinations_TokenWithMirrorAllowed is the positive half:
// a token with the mirror ability AND the repo in its scope can
// create + list + get + patch + delete destinations through the
// subscriber-facing path. Round-trip proves the helpers are shared
// with the admin path rather than silently diverging.
func TestRepoDestinations_TokenWithMirrorAllowed(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "addresses", []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}

	// Create
	req := bearer(t, http.MethodPost, "/api/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatalf("created dest missing id: %+v", created)
	}

	// List
	req = bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Delete
	req = bearer(t, http.MethodDelete, "/api/repos/addresses/destinations/"+created.ID, nil, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_TokenWithoutMirrorDenied locks in the
// read/write split: a token in repo scope but without the mirror
// ability can read the destinations list (it's informational data
// scoped to a repo the token already reaches) but cannot write —
// POST/PATCH/DELETE/sync still 403. The mirror ability gates only
// the writes, never the read.
func TestRepoDestinations_TokenWithoutMirrorDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET without mirror ability want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = bearer(t, http.MethodPost, "/api/repos/addresses/destinations",
		map[string]any{"url": "https://x.com/r.git", "credential_name": "github-personal"}, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without mirror ability want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_GETSingleAllowedWithoutMirror locks in the
// read/write split for the per-id GET (`/destinations/{id}`), not
// just the list. A no-mirror token in repo scope can read either
// shape; the gate split applies to all GETs uniformly so a future
// refactor can't accidentally retighten one of them.
func TestRepoDestinations_GETSingleAllowedWithoutMirror(t *testing.T) {
	srv := subscriberTestServer(t)
	// Seed a destination via the store (no admin/auth flow needed).
	dest, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations/"+dest.ID, nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET single without mirror ability want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != dest.ID || got.URL != dest.URL {
		t.Fatalf("response did not round-trip: %+v", got)
	}
}

// TestRepoDestinations_AllWritesDeniedWithoutMirror exercises every
// write verb (PATCH, DELETE, POST .../sync, POST .../status/refresh)
// with a no-mirror token to guarantee the role+ability gate fires
// for each one independently. The list-POST case is covered by
// TokenWithoutMirrorDenied; this test fans out across the remaining
// verbs so a one-off relaxation of any single handler entry doesn't
// slip past CI.
func TestRepoDestinations_AllWritesDeniedWithoutMirror(t *testing.T) {
	srv := subscriberTestServer(t)
	dest, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"PATCH", http.MethodPatch, "/api/repos/addresses/destinations/" + dest.ID,
			map[string]any{"enabled": false}},
		{"DELETE", http.MethodDelete, "/api/repos/addresses/destinations/" + dest.ID, nil},
		{"POST sync", http.MethodPost, "/api/repos/addresses/destinations/" + dest.ID + "/sync", nil},
		{"POST status/refresh", http.MethodPost, "/api/repos/addresses/destinations/" + dest.ID + "/status/refresh", nil},
	}
	for _, tc := range cases {
		req := bearer(t, tc.method, tc.path, tc.body, token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s without mirror ability want 403, got %d body=%s",
				tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestRepoDestinations_GETSingleOutOfScopeDenied confirms the read
// gate still enforces repo scope on the per-id GET — having a token
// for *some* repo doesn't open up reads on every repo's
// destinations. Pairs with GETSingleAllowedWithoutMirror: the
// ability gate dropped, the scope gate did not.
func TestRepoDestinations_GETSingleOutOfScopeDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	dest, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Token scoped to a different repo.
	token, err := srv.tokenStrategy.Issue("alice", "some-other-repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations/"+dest.ID, nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET single out-of-scope want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_AllWritesDeniedAsRegular fans the role gate
// across every write verb (PATCH, DELETE, POST .../sync) the same
// way AllWritesDeniedWithoutMirror fans the ability gate. Both
// fences must fire independently for each verb so a relaxation of
// one doesn't quietly leak through another.
func TestRepoDestinations_AllWritesDeniedAsRegular(t *testing.T) {
	srv := subscriberTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "bob",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	dest, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Stale-key shape: regular account, but key carries mirror.
	token, err := srv.tokenStrategy.Issue("bob", "addresses", []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"PATCH", http.MethodPatch, "/api/repos/addresses/destinations/" + dest.ID,
			map[string]any{"enabled": false}},
		{"DELETE", http.MethodDelete, "/api/repos/addresses/destinations/" + dest.ID, nil},
		{"POST sync", http.MethodPost, "/api/repos/addresses/destinations/" + dest.ID + "/sync", nil},
		{"POST status/refresh", http.MethodPost, "/api/repos/addresses/destinations/" + dest.ID + "/status/refresh", nil},
	}
	for _, tc := range cases {
		req := bearer(t, tc.method, tc.path, tc.body, token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as regular want 403, got %d body=%s",
				tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestRepoDestinations_RegularRoleDenied is the role-gate negative:
// even a token with the mirror ability AND the right repo scope is
// rejected on writes if the issuing account's role is `regular`.
// Mirroring writes are fenced to admin + maintainer roles regardless
// of stale ability bits on previously-issued keys (see accounts.md
// §1). The role gate, like the ability gate, only applies to writes
// — reads stay open at the repo-scope level.
func TestRepoDestinations_RegularRoleDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "bob",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	// Issue directly through the strategy to bypass the issue-time
	// ability check — simulates a stale key from before the role
	// fence existed. The runtime gate must still deny writes.
	token, err := srv.tokenStrategy.Issue("bob", "addresses", []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET as regular want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = bearer(t, http.MethodPost, "/api/repos/addresses/destinations",
		map[string]any{"url": "https://x.com/r.git", "credential_name": "github-personal"}, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST as regular want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_UnscopedRepoDenied guards against a token with
// the mirror ability but a different repo's scope slipping through.
// Ability and scope are AND'd — one without the other must fail.
func TestRepoDestinations_UnscopedRepoDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "some-other-repo", []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET out-of-scope repo want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_NoTokenUnauthorized keeps the auth-required
// contract on record — without credentials, you never reach the
// policy layer at all.
func TestRepoDestinations_NoTokenUnauthorized(t *testing.T) {
	srv := subscriberTestServer(t)

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoDestinations_AdminPathStillWorks proves the new subscriber
// path did not inadvertently break the existing admin-session path.
// The admin setup creates and reads a destination the same way
// TestDestinations_CreateThenList does — regression fence.
func TestRepoDestinations_AdminPathStillWorks(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin POST regressed: got %d body=%s", rec.Code, rec.Body.String())
	}
}
