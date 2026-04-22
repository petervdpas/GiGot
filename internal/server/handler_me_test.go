package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
)

// TestMe_RequiresSession — no cookie, no profile.
func TestMe_RequiresSession(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/api/me", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestMe_RegularUserSeesOwnProfileOnly covers the core promise: a
// regular user sees their own row and their own tokens, and nothing
// else. Seeded with two accounts + two tokens to make sure the
// filtering actually filters.
func TestMe_RegularUserSeesOwnProfileOnly(t *testing.T) {
	srv := testServer(t)
	// Alice — the caller. Regular, OAuth-style (microsoft).
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:    "microsoft",
		Identifier:  "alice-msa",
		Role:        accounts.RoleRegular,
		DisplayName: "Alice Example",
	}); err != nil {
		t.Fatal(err)
	}
	// Bob — someone else. Local, regular. Shouldn't appear in Alice's /me.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}

	// Two tokens: one for Alice, one for Bob. Alice's /me must only
	// surface hers.
	aliceTok, err := srv.tokenStrategy.Issue("microsoft:alice-msa", []string{"repo-a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("bob", []string{"repo-b"}, nil); err != nil {
		t.Fatal(err)
	}

	sessObj, err := srv.sessionStrategy.Create("microsoft", "alice-msa")
	if err != nil {
		t.Fatal(err)
	}
	sess := &http.Cookie{Name: auth.SessionCookieName, Value: sessObj.ID}
	rec := do(t, srv, http.MethodGet, "/api/me", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "alice-msa" || got.Provider != "microsoft" {
		t.Fatalf("identity = %+v", got)
	}
	if got.DisplayName != "Alice Example" {
		t.Fatalf("display_name = %q, want %q", got.DisplayName, "Alice Example")
	}
	if got.Role != accounts.RoleRegular {
		t.Fatalf("role = %q, want regular", got.Role)
	}
	if len(got.Subscriptions) != 1 {
		t.Fatalf("want 1 subscription (alice's), got %d: %+v", len(got.Subscriptions), got.Subscriptions)
	}
	if got.Subscriptions[0].Token != aliceTok {
		t.Fatal("surfaced the wrong token")
	}
	if got.Subscriptions[0].Repos[0] != "repo-a" {
		t.Fatalf("repos = %v", got.Subscriptions[0].Repos)
	}
}

// TestMe_AdminAlsoHasProfile — admins see the same /me payload shape
// as regulars. The page is role-agnostic; role only differs by the
// "Admin console" link rendering.
func TestMe_AdminAlsoHasProfile(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/me", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Role != accounts.RoleAdmin {
		t.Fatalf("admin caller saw role=%q", got.Role)
	}
	if got.Subscriptions == nil {
		t.Fatal("subscriptions should be [] not null")
	}
}

// TestMe_LegacyBareTokenMatchesLocalCaller proves we respect the
// bare-token back-compat shape: a token whose Username is "alice"
// (no provider prefix) resolves to (local, alice) and surfaces for
// a local-provider caller. Without this, old installs with legacy
// tokens would silently stop showing them on /me.
func TestMe_LegacyBareTokenMatchesLocalCaller(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "alice", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("alice", []string{"repo-a"}, nil); err != nil {
		t.Fatal(err)
	}
	sessObj, _ := srv.sessionStrategy.Create("local", "alice")
	sess := &http.Cookie{Name: auth.SessionCookieName, Value: sessObj.ID}

	rec := do(t, srv, http.MethodGet, "/api/me", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Subscriptions) != 1 {
		t.Fatalf("legacy bare token must surface on /me for local caller; got %d", len(got.Subscriptions))
	}
}

// TestMe_NonGetRejected — keep the handler honest.
func TestMe_NonGetRejected(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/me", map[string]any{}, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}
