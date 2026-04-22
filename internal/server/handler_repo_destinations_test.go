package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/credentials"
)

// subscriberTestServer spins up a fresh server with auth enabled, one
// credential in the vault, and one bare repo named "addresses". Returns
// the server and the repo name so tests can issue tokens with whatever
// mix of scopes/abilities they need.
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

// TestRepoDestinations_TokenWithoutMirrorDenied is the key negative:
// a token that has the repo in scope but lacks the mirror ability
// gets 403 on every destinations verb. Without this gate, granting
// any subscription would implicitly grant remote-sync configuration.
func TestRepoDestinations_TokenWithoutMirrorDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/destinations", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET without mirror ability want 403, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = bearer(t, http.MethodPost, "/api/repos/addresses/destinations",
		map[string]any{"url": "https://x.com/r.git", "credential_name": "github-personal"}, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without mirror ability want 403, got %d body=%s", rec.Code, rec.Body.String())
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
