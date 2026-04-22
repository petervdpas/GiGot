package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/auth/oauth"
)

// stubProvider is an in-memory oauth.Provider used to drive the
// /admin/login/<name>/callback branch without a real IdP round
// trip. AuthURL just echoes the state (so the test can read it back
// for the callback call), and ExchangeAndClaim returns a canned
// claim keyed by provider name.
type stubProvider struct {
	name        string
	display     string
	register    bool
	claim       oauth.Claim
	exchangeErr error
}

func (p *stubProvider) Name() string         { return p.name }
func (p *stubProvider) DisplayName() string  { return p.display }
func (p *stubProvider) AllowsRegister() bool { return p.register }
func (p *stubProvider) AuthURL(redirectURI, state, nonce, codeChallenge string) string {
	return "https://stub.example/authorize?state=" + state
}
func (p *stubProvider) ExchangeAndClaim(ctx context.Context, code, verifier, redirectURI, nonce string) (oauth.Claim, error) {
	if p.exchangeErr != nil {
		return oauth.Claim{}, p.exchangeErr
	}
	return p.claim, nil
}

// installStubProviders rebuilds the server's oauth registry in place
// with the given stub providers and a fresh short-TTL state store.
// Can't go through oauth.Build (needs a live discovery URL), so
// surgery on the private fields is fine here.
func installStubProviders(srv *Server, providers ...*stubProvider) {
	reg := &fakeRegistry{entries: map[string]oauth.Provider{}}
	for _, p := range providers {
		reg.entries[p.Name()] = p
	}
	srv.oauthProviders = reg.toRegistry()
	srv.oauthState = oauth.NewStateStore(time.Minute)
}

// fakeRegistry exists only to give us a *oauth.Registry with custom
// entries. The package-private map on Registry is set via a little
// helper function we add below in oauth_registry_testing.go.
type fakeRegistry struct{ entries map[string]oauth.Provider }

func (f *fakeRegistry) toRegistry() *oauth.Registry {
	return oauth.RegistryForTest(f.entries)
}

func extractState(t *testing.T, location string) string {
	t.Helper()
	i := strings.Index(location, "state=")
	if i < 0 {
		t.Fatalf("no state= in %q", location)
	}
	state := location[i+len("state="):]
	if amp := strings.Index(state, "&"); amp >= 0 {
		state = state[:amp]
	}
	return state
}

// TestOAuth_StartRedirects verifies /admin/login/<name> issues a
// 302 to the provider with a state parameter the handler can later
// match at callback time.
func TestOAuth_StartRedirects(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{name: "github", display: "GitHub"})

	req := httptest.NewRequest(http.MethodGet, "/admin/login/github", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://stub.example/authorize") {
		t.Fatalf("bad redirect: %q", loc)
	}
	if extractState(t, loc) == "" {
		t.Fatalf("missing state in redirect")
	}
}

func TestOAuth_UnknownProviderIs404(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{name: "github"})

	req := httptest.NewRequest(http.MethodGet, "/admin/login/nope", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestOAuth_CallbackAutoRegistersRegularAndLandsOnUser is the
// load-bearing regular-user path: first-time OAuth login → account
// auto-registered → session cookie minted → redirect to /user (not
// /admin/repositories, which requireAdminSession would 401 them out
// of). The role guard for the admin console lives in
// requireAdminSession; the landing page is the visible half.
func TestOAuth_CallbackAutoRegistersRegularAndLandsOnUser(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{
		name:     "entra",
		display:  "Entra",
		register: true,
		claim: oauth.Claim{
			Identifier:  "peter-at-work",
			DisplayName: "Peter (work)",
		},
	})

	startReq := httptest.NewRequest(http.MethodGet, "/admin/login/entra", nil)
	startRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(startRec, startReq)
	state := extractState(t, startRec.Header().Get("Location"))

	cbReq := httptest.NewRequest(http.MethodGet,
		"/admin/login/entra/callback?state="+state+"&code=code-abc", nil)
	cbRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback want 302, got %d body=%s", cbRec.Code, cbRec.Body.String())
	}
	if got := cbRec.Header().Get("Location"); got != "/user" {
		t.Fatalf("regular user must land on /user, got %q", got)
	}
	var gotCookie *http.Cookie
	for _, c := range cbRec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			gotCookie = c
			break
		}
	}
	if gotCookie == nil {
		t.Fatal("session cookie MUST be minted for regular OAuth users (used by /user)")
	}

	acc, err := srv.accounts.Get("entra", "peter-at-work")
	if err != nil {
		t.Fatalf("auto-register missed: %v", err)
	}
	if acc.Role != accounts.RoleRegular {
		t.Fatalf("role=%q, want regular", acc.Role)
	}
	if acc.DisplayName != "Peter (work)" {
		t.Fatalf("display name not propagated: %q", acc.DisplayName)
	}
}

// TestOAuth_RegularSessionCannotReachAdminPages covers the guard
// side: a regular user HAS a valid session cookie (they reached
// /user legitimately), but admin routes still return 401. This is
// the security invariant — the /user landing doesn't weaken the
// admin gate.
func TestOAuth_RegularSessionCannotReachAdminPages(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: "entra", Identifier: "peter-at-work", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	sessObj, _ := srv.sessionStrategy.Create("entra", "peter-at-work")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessObj.ID})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin endpoint must reject regular session cookie; got %d", rec.Code)
	}

	// But /api/me accepts the same cookie (role-agnostic).
	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessObj.ID})
	meRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/api/me must accept regular session; got %d", meRec.Code)
	}
}

// TestOAuth_CallbackAdminLoginMintsSession is the happy path: an
// OAuth identity that already resolves to a pre-existing admin
// account DOES get a session cookie and the redirect to
// /admin/repositories. This covers the case where an admin pre-creates
// the row (or an existing regular was promoted) and then logs in.
func TestOAuth_CallbackAdminLoginMintsSession(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{
		name: "entra", display: "Entra", register: true,
		claim: oauth.Claim{Identifier: "boss", DisplayName: "The Boss"},
	})
	// Pre-create the account as admin so OAuth resolves to an admin row.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:    "entra",
		Identifier:  "boss",
		Role:        accounts.RoleAdmin,
		DisplayName: "The Boss",
	}); err != nil {
		t.Fatal(err)
	}

	startReq := httptest.NewRequest(http.MethodGet, "/admin/login/entra", nil)
	startRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(startRec, startReq)
	state := extractState(t, startRec.Header().Get("Location"))

	cbReq := httptest.NewRequest(http.MethodGet,
		"/admin/login/entra/callback?state="+state+"&code=code-abc", nil)
	cbRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback want 302, got %d body=%s", cbRec.Code, cbRec.Body.String())
	}
	if cbRec.Header().Get("Location") != "/admin/repositories" {
		t.Fatalf("callback redirect = %q", cbRec.Header().Get("Location"))
	}
	var gotCookie *http.Cookie
	for _, c := range cbRec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			gotCookie = c
			break
		}
	}
	if gotCookie == nil {
		t.Fatal("admin OAuth must mint a session cookie")
	}
}

func TestOAuth_CallbackUnknownUserRejectedWhenRegisterDisabled(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{
		name:     "entra",
		display:  "Entra",
		register: false,
		claim:    oauth.Claim{Identifier: "no-account-here"},
	})

	startReq := httptest.NewRequest(http.MethodGet, "/admin/login/entra", nil)
	startRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(startRec, startReq)
	state := extractState(t, startRec.Header().Get("Location"))

	cbReq := httptest.NewRequest(http.MethodGet,
		"/admin/login/entra/callback?state="+state+"&code=code-abc", nil)
	cbRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cbRec, cbReq)

	if cbRec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", cbRec.Code, cbRec.Body.String())
	}
	if srv.accounts.Has("entra", "no-account-here") {
		t.Fatal("account must NOT be auto-created when allow_register=false")
	}
}

func TestOAuth_CallbackReplayIsRejected(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv, &stubProvider{
		name: "entra", display: "Entra", register: true,
		claim: oauth.Claim{Identifier: "peter"},
	})
	// Pre-create as admin so the first callback gets past the
	// "regular accounts can't mint a session" guard; this test's
	// assertion is about state one-shot semantics, not role gating.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: "entra", Identifier: "peter", Role: accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	startReq := httptest.NewRequest(http.MethodGet, "/admin/login/entra", nil)
	startRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(startRec, startReq)
	state := extractState(t, startRec.Header().Get("Location"))

	// First callback succeeds.
	first := httptest.NewRecorder()
	srv.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet,
		"/admin/login/entra/callback?state="+state+"&code=c", nil))
	if first.Code != http.StatusFound {
		t.Fatalf("first callback want 302, got %d", first.Code)
	}

	// Second attempt with the same state MUST fail — state is
	// one-shot per §8.
	second := httptest.NewRecorder()
	srv.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet,
		"/admin/login/entra/callback?state="+state+"&code=c", nil))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second callback want 401 (one-shot), got %d", second.Code)
	}
}

func TestProvidersEndpoint_ListsEnabledProviders(t *testing.T) {
	srv := testServer(t)
	installStubProviders(srv,
		&stubProvider{name: "github", display: "GitHub"},
		&stubProvider{name: "entra", display: "Entra"},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/providers", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp OAuthProvidersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(resp.Providers))
	}
	names := []string{resp.Providers[0].Name, resp.Providers[1].Name}
	// Registry.Providers() returns a stable [github, entra, microsoft]
	// order regardless of insert order.
	if names[0] != "github" || names[1] != "entra" {
		t.Fatalf("provider order = %v, want [github entra]", names)
	}
	if resp.Providers[0].LoginURL != "/admin/login/github" {
		t.Fatalf("bad login_url: %q", resp.Providers[0].LoginURL)
	}
}
