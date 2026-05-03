package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	aliceTok, err := srv.tokenStrategy.Issue("microsoft:alice-msa", "repo-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("bob", "repo-b", nil); err != nil {
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
	if got.Subscriptions[0].Repo != "repo-a" {
		t.Fatalf("repo = %q, want %q", got.Subscriptions[0].Repo, "repo-a")
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

// TestMe_ReturnsEmail covers the email round-trip from /api/me.
// Formidable's "signed in as" header reads me.email; without this
// guard, a stored Email could silently stop appearing in the
// response without the unit suite catching it.
func TestMe_ReturnsEmail(t *testing.T) {
	srv, sess := adminTestServer(t)
	// Update the seeded admin to carry an email so /api/me reads it
	// off the row. adminTestServer's bootstrap doesn't set one.
	if existing, err := srv.accounts.Get(accounts.ProviderLocal, "alice"); err == nil {
		existing.Email = "alice@example.com"
		if _, err := srv.accounts.Put(*existing); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodGet, "/api/me", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Email != "alice@example.com" {
		t.Fatalf("Email = %q, want round-tripped from store", got.Email)
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
	if _, err := srv.tokenStrategy.Issue("alice", "repo-a", nil); err != nil {
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

// TestMe_BearerReturnsOwnSubscription locks in the bearer-auth path:
// a token caller (no session cookie) gets the same response shape
// as a session caller, but Subscriptions is filtered to the single
// token presented. Lets API clients (Formidable) introspect their
// own role + abilities without probing 403s.
func TestMe_BearerReturnsOwnSubscription(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:    accounts.ProviderLocal,
		Identifier:  "alice",
		Role:        accounts.RoleMaintainer,
		DisplayName: "Alice Example",
		Email:       "alice@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("alice", "repo-a", []string{auth.AbilityMirror})
	if err != nil {
		t.Fatal(err)
	}
	// A second token for someone else — must NOT surface on alice's /me.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("bob", "repo-b", nil); err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/me", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != accounts.ProviderLocal {
		t.Fatalf("provider = %q, want %q", got.Provider, accounts.ProviderLocal)
	}
	if got.Role != accounts.RoleMaintainer {
		t.Fatalf("role = %q, want maintainer", got.Role)
	}
	if got.DisplayName != "Alice Example" || got.Email != "alice@example.com" {
		t.Fatalf("profile not round-tripped: %+v", got)
	}
	if len(got.Subscriptions) != 1 {
		t.Fatalf("want 1 subscription (the bearer's own), got %d: %+v", len(got.Subscriptions), got.Subscriptions)
	}
	sub := got.Subscriptions[0]
	if sub.Token != tok {
		t.Fatalf("surfaced wrong token: %q", sub.Token)
	}
	if sub.Repo != "repo-a" {
		t.Fatalf("repo = %q, want %q", sub.Repo, "repo-a")
	}
	if len(sub.Abilities) != 1 || sub.Abilities[0] != auth.AbilityMirror {
		t.Fatalf("abilities = %v, want [mirror]", sub.Abilities)
	}
}

// TestMe_BearerWithoutAccountFallsBackToRegular — a token whose
// stored Username doesn't resolve to an existing account row still
// authenticates (the token itself is the credential), but role
// defaults to "regular" so role-fenced UI doesn't accidentally
// unlock. Mirrors the session path's same fallback.
func TestMe_BearerWithoutAccountFallsBackToRegular(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	// Issue a token without ever creating the matching account.
	tok, err := srv.tokenStrategy.Issue("ghost", "repo-x", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/me", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Role != accounts.RoleRegular {
		t.Fatalf("role = %q, want regular fallback", got.Role)
	}
	if len(got.Subscriptions) != 1 || got.Subscriptions[0].HasAccount {
		t.Fatalf("subscription has_account should be false; got %+v", got.Subscriptions)
	}
}

// TestMe_InvalidBearerUnauthorized — a malformed/unknown bearer is
// treated like no auth at all (401), not a partial response.
func TestMe_InvalidBearerUnauthorized(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	req := bearer(t, http.MethodGet, "/api/me", nil, "not-a-real-token")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestMe_BearerWithScopedUsername proves the realistic OAuth shape
// (provider:identifier) round-trips: a token Username like
// "github:alice@example.com" must resolve to (github, alice@...) and
// surface the github account's profile + role. Without this, only
// legacy bare-string tokens would work for bearer /me.
func TestMe_BearerWithScopedUsername(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:    "github",
		Identifier:  "alice@example.com",
		Role:        accounts.RoleAdmin,
		DisplayName: "Alice on GitHub",
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("github:alice@example.com", "repo-a", []string{auth.AbilityMirror})
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodGet, "/api/me", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "github" {
		t.Fatalf("provider = %q, want github", got.Provider)
	}
	if got.Role != accounts.RoleAdmin {
		t.Fatalf("role = %q, want admin", got.Role)
	}
	if got.DisplayName != "Alice on GitHub" {
		t.Fatalf("display_name = %q", got.DisplayName)
	}
	if len(got.Subscriptions) != 1 || got.Subscriptions[0].Token != tok {
		t.Fatalf("subscriptions = %+v", got.Subscriptions)
	}
}

// TestMe_SessionTakesPrecedenceOverBearer locks in the auth-mode
// priority when both are presented: a logged-in admin who happens to
// also have a regular bearer in the same request gets the session
// view (full subscriptions list), not the bearer-filtered one. Keeps
// the cookie-bearing browser path the canonical answer for /me.
func TestMe_SessionTakesPrecedenceOverBearer(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.auth.SetEnabled(true)
	// An unrelated bearer token in the same request — must be ignored
	// while a valid session is present.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	bobTok, err := srv.tokenStrategy.Issue("bob", "repo-b", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(sess)
	req.Header.Set("Authorization", "Bearer "+bobTok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Session was admin "alice", not bearer holder "bob".
	if got.Username == "bob" {
		t.Fatalf("bearer leaked through; got username=%q", got.Username)
	}
	if got.Role != accounts.RoleAdmin {
		t.Fatalf("session admin role lost; got %q", got.Role)
	}
}

// TestMe_BearerNonGetRejected — the method gate fires uniformly for
// both auth modes. A bearer caller doing POST /api/me must get 405,
// not slip through because the session-only handler exited early.
func TestMe_BearerNonGetRejected(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "alice", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("alice", "repo-a", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodPost, "/api/me", map[string]any{}, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d body=%s", rec.Code, rec.Body.String())
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
