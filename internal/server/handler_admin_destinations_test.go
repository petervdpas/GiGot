package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/credentials"
)

// adminTestServer spins up a fresh Server with one admin account and
// one seeded credential in the vault, returning the server plus a
// session cookie ready to attach to authenticated requests.
func adminTestServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "alice",
		Role:       accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.SetPassword("alice", "pw"); err != nil {
		t.Fatal(err)
	}
	sess, err := srv.sessionStrategy.Create("local", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: "github-personal", Kind: "pat", Secret: "ghp_x",
	}); err != nil {
		t.Fatal(err)
	}
	return srv, &http.Cookie{Name: auth.SessionCookieName, Value: sess.ID}
}

// do is a tiny request helper: build request, attach session cookie,
// serve, return the recorder.
func do(t *testing.T, srv *Server, method, path string, body any, sess *http.Cookie) *httptest.ResponseRecorder {
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
	if sess != nil {
		req.AddCookie(sess)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDestinations_RequireAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodGet, "/api/admin/repos/addresses/destinations", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without session, got %d", rec.Code)
	}
}

func TestDestinations_RepoMustExist(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodGet, "/api/admin/repos/nope/destinations", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown repo, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDestinations_CreateThenList(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !created.Enabled {
		t.Fatalf("created dest looks wrong: %+v", created)
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/repos/addresses/destinations", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}
	var list DestinationListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Destinations[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestDestinations_CreateRejectsUnknownCredential(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "does-not-exist",
		}, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown credential, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDestinations_CreateRequiresFields(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing url", map[string]any{"credential_name": "github-personal"}},
		{"missing credential_name", map[string]any{"url": "https://x"}},
		{"empty body", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations", tc.body, sess)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDestinations_PatchAndDelete(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	// seed one
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// PATCH: disable
	falsy := false
	rec = do(t, srv, http.MethodPatch,
		"/api/admin/repos/addresses/destinations/"+created.ID,
		map[string]any{"enabled": &falsy}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var patched DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Enabled {
		t.Fatal("PATCH did not disable destination")
	}
	if patched.ID != created.ID {
		t.Fatal("PATCH rewrote ID")
	}

	// PATCH: re-enable. Proves the toggle cycles both ways — the
	// click-to-toggle badge in the admin UI relies on flipping either
	// direction via the same PATCH endpoint, not just disable.
	truthy := true
	rec = do(t, srv, http.MethodPatch,
		"/api/admin/repos/addresses/destinations/"+created.ID,
		map[string]any{"enabled": &truthy}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH re-enable want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if !patched.Enabled {
		t.Fatal("PATCH re-enable did not flip enabled back to true")
	}

	// DELETE
	rec = do(t, srv, http.MethodDelete,
		"/api/admin/repos/addresses/destinations/"+created.ID, nil, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d", rec.Code)
	}
	rec = do(t, srv, http.MethodDelete,
		"/api/admin/repos/addresses/destinations/"+created.ID, nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE want 404, got %d", rec.Code)
	}
}

// TestDestinations_PatchPreservesOmittedEnabled covers the
// edit-form-doesn't-send-enabled contract the cleaned-up admin UI
// depends on. The edit form in admin.js now POSTs only
// {url, credential_name}; the enabled flag is managed by the
// click-to-toggle badge, not the form. If PATCH were to silently
// reset enabled=false to the zero value (false) on any request that
// omitted it, every URL-edit on a disabled destination would
// secretly re-enable the wrong one. Lock down "nil pointer means
// unchanged" end-to-end through the handler.
func TestDestinations_PatchPreservesOmittedEnabled(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}

	// Create + disable so enabled=false is load-bearing in the assertion.
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	falsy := false
	do(t, srv, http.MethodPatch,
		"/api/admin/repos/addresses/destinations/"+created.ID,
		map[string]any{"enabled": &falsy}, sess)

	// PATCH with only `url` — no `enabled` in the body. The stored
	// enabled flag must not flip back to the default.
	rec = do(t, srv, http.MethodPatch,
		"/api/admin/repos/addresses/destinations/"+created.ID,
		map[string]any{"url": "https://github.com/alice/addresses-renamed.git"}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH url-only want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var patched DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Enabled {
		t.Fatal("PATCH that omits enabled must leave disabled destinations disabled; got enabled=true")
	}
	if patched.URL != "https://github.com/alice/addresses-renamed.git" {
		t.Fatalf("URL should have been updated; got %q", patched.URL)
	}
}

func TestCredentials_DeleteBlockedWhenReferenced(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	// Attach github-personal to a destination on addresses.
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed POST failed: %d %s", rec.Code, rec.Body.String())
	}

	// Now try to delete the credential. Should be 409 with ref_repos.
	rec = do(t, srv, http.MethodDelete, "/api/admin/credentials/github-personal", nil, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var conflict CredentialDeleteConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if len(conflict.RefRepos) != 1 || conflict.RefRepos[0] != "addresses" {
		t.Fatalf("want ref_repos=[addresses], got %+v", conflict)
	}

	// Credential must still exist.
	if _, err := srv.credentials.Get("github-personal"); err != nil {
		t.Fatalf("credential disappeared despite 409: %v", err)
	}
}

func TestCredentials_DeleteSucceedsAfterDestinationRemoved(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var dest DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &dest)

	// Clear the reference by deleting the destination.
	rec = do(t, srv, http.MethodDelete,
		"/api/admin/repos/addresses/destinations/"+dest.ID, nil, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("destination delete failed: %d", rec.Code)
	}

	// Now the credential delete should go through.
	rec = do(t, srv, http.MethodDelete, "/api/admin/credentials/github-personal", nil, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 after reference cleared, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := srv.credentials.Get("github-personal"); err == nil {
		t.Fatal("credential should be gone")
	}
}

func TestRepoDelete_CleansDestinations(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	// Seed two destinations on the repo.
	for _, u := range []string{"https://a.example.git", "https://b.example.git"} {
		rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
			map[string]any{"url": u, "credential_name": "github-personal"}, sess)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed failed: %d %s", rec.Code, rec.Body.String())
		}
	}
	if got := srv.destinations.Count(); got != 2 {
		t.Fatalf("precondition: want 2 destinations, got %d", got)
	}

	// Delete the repo via the admin route (token auth is off by default
	// in testServer, so the route is open to session callers too).
	rec := do(t, srv, http.MethodDelete, "/api/repos/addresses", nil, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repo DELETE want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := srv.destinations.Count(); got != 0 {
		t.Fatalf("destinations should be cleaned on repo delete, got %d remaining", got)
	}
}

func TestDestinations_BadPathShapes(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	// Trailing slash with no id should still hit list — acceptable.
	rec := do(t, srv, http.MethodGet, "/api/admin/repos/addresses/destinations/", nil, sess)
	// Either 200 (empty list) or 400 is defensible; we chose list-with-empty-id.
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for /destinations/ trailing slash, got %d body=%s", rec.Code, rec.Body.String())
	}

	// {id}/{action} is valid shape now that /sync exists. An unknown
	// action surfaces as 404 ("unknown destination action"), not 400.
	rec = do(t, srv, http.MethodGet, "/api/admin/repos/addresses/destinations/a/b", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown action, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Five segments past /destinations is still not a thing we serve.
	rec = do(t, srv, http.MethodGet, "/api/admin/repos/addresses/destinations/a/b/c", nil, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for too-deep path, got %d", rec.Code)
	}
}
