package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// TestRepoContext_BearerHappyPath is the bootstrap-by-design case:
// a Formidable client connects with a subscription key and reads
// user/subscription/repo in one call. Asserts every top-level field
// is populated so the contract can't quietly drop a key.
func TestRepoContext_BearerHappyPath(t *testing.T) {
	srv := subscriberTestServer(t)
	// Add an enabled mirror destination so the count surfaces non-zero.
	if _, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://github.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        true,
	}); err != nil {
		t.Fatal(err)
	}
	// And a disabled one — total = 2, auto = 1.
	if _, err := srv.destinations.Add("addresses", destinations.Destination{
		URL:            "https://gitlab.com/alice/addresses.git",
		CredentialName: "github-personal",
		Enabled:        false,
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("alice", "addresses", []string{auth.AbilityMirror})
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/context", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got RepoContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	// User
	if got.User.Username != "alice" {
		t.Fatalf("user.username = %q", got.User.Username)
	}
	if got.User.Provider != accounts.ProviderLocal {
		t.Fatalf("user.provider = %q", got.User.Provider)
	}
	if got.User.Role != accounts.RoleMaintainer {
		t.Fatalf("user.role = %q (want maintainer)", got.User.Role)
	}

	// Subscription
	if got.Subscription.Repo != "addresses" {
		t.Fatalf("subscription.repo = %q", got.Subscription.Repo)
	}
	if len(got.Subscription.Abilities) != 1 || got.Subscription.Abilities[0] != auth.AbilityMirror {
		t.Fatalf("subscription.abilities = %v", got.Subscription.Abilities)
	}

	// Repo
	if got.Repo.Name != "addresses" {
		t.Fatalf("repo.name = %q", got.Repo.Name)
	}
	if !got.Repo.Empty {
		t.Fatalf("freshly InitBare'd repo should be empty")
	}
	if got.Repo.Destinations.Total != 2 {
		t.Fatalf("repo.destinations.total = %d, want 2", got.Repo.Destinations.Total)
	}
	if got.Repo.Destinations.AutoMirrorEnabled != 1 {
		t.Fatalf("repo.destinations.auto_mirror_enabled = %d, want 1",
			got.Repo.Destinations.AutoMirrorEnabled)
	}
}

// TestRepoContext_NoMirrorAbilityStillReports — the bootstrap call
// is read-only and ungated by ability, so a no-mirror token gets the
// same shape; abilities is just the empty list. Without this, a
// client with no ability bits would 403 here and have no way to
// build its UI.
func TestRepoContext_NoMirrorAbilityStillReports(t *testing.T) {
	srv := subscriberTestServer(t)
	if _, err := srv.destinations.Add("addresses", destinations.Destination{
		URL: "https://x", CredentialName: "github-personal", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("alice", "addresses", nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/repos/addresses/context", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got RepoContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Subscription.Abilities == nil {
		t.Fatal("abilities should be [] not null — clients depend on the JSON shape")
	}
	if len(got.Subscription.Abilities) != 0 {
		t.Fatalf("abilities = %v, want []", got.Subscription.Abilities)
	}
	// Repo state still surfaces — destinations count is informational.
	if got.Repo.Destinations.Total != 1 {
		t.Fatalf("repo.destinations.total = %d, want 1", got.Repo.Destinations.Total)
	}
}

// TestRepoContext_OutOfScopeDenied — the read gate is repo-scoped.
// A token bound to a different repo cannot bootstrap against this
// one regardless of ability bits.
func TestRepoContext_OutOfScopeDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "some-other-repo", []string{auth.AbilityMirror})
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/repos/addresses/context", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoContext_UnknownRepoNotFound — a valid token whose scope
// matches the requested name still 404s if the repo doesn't exist
// on disk. Lets clients distinguish "you can't reach it" from "it
// isn't there."
func TestRepoContext_UnknownRepoNotFound(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "ghost-repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/repos/ghost-repo/context", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoContext_NoAuthUnauthorized — bootstrap requires auth like
// every other repo route.
func TestRepoContext_NoAuthUnauthorized(t *testing.T) {
	srv := subscriberTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/addresses/context", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoContext_NonGetRejected — keep the handler honest.
func TestRepoContext_NonGetRejected(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodPost, "/api/repos/addresses/context",
		map[string]any{}, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d body=%s", rec.Code, rec.Body.String())
	}
}
