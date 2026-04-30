package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/credentials"
)

// credentialsNamesTestServer extends subscriberTestServer with a
// second credential and a regular account so role-based assertions
// have something to deny against. Reuses the maintainer "alice"
// account already seeded in subscriberTestServer.
func credentialsNamesTestServer(t *testing.T) *Server {
	t.Helper()
	srv := subscriberTestServer(t)
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: "azure-pat", Kind: "pat", Secret: "az_x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "regular-bob",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestCredentialsNames_MaintainerSees pairs the positive role-gate
// case with the negative below. A maintainer-issued bearer token
// receives the names + kinds, never the secrets. Encodes the §2.6
// rule that mirror-wiring UIs need vault names without admin reach.
func TestCredentialsNames_MaintainerSees(t *testing.T) {
	srv := credentialsNamesTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/credentials/names", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("maintainer GET want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp CredentialNameListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 || len(resp.Credentials) != 2 {
		t.Fatalf("want 2 credential refs, got count=%d len=%d", resp.Count, len(resp.Credentials))
	}
	// Belt-and-braces: the response shape must not surface secrets.
	body := rec.Body.String()
	for _, leak := range []string{"ghp_x", "az_x", "secret", "expires", "last_used"} {
		if strings.Contains(body, leak) {
			t.Errorf("response body leaked %q: %s", leak, body)
		}
	}
}

// TestCredentialsNames_RegularDenied is the role-gate negative.
// A token for a regular account must be rejected with 403 even
// though no ability bit gate exists on this endpoint — the role IS
// the gate.
func TestCredentialsNames_RegularDenied(t *testing.T) {
	srv := credentialsNamesTestServer(t)
	token, err := srv.tokenStrategy.Issue("regular-bob", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/credentials/names", nil, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("regular GET want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCredentialsNames_AnonymousUnauthorized keeps the auth-required
// contract on record. Without credentials we never reach the role
// gate at all; the auth middleware writes 401 first.
func TestCredentialsNames_AnonymousUnauthorized(t *testing.T) {
	srv := credentialsNamesTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/credentials/names", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCredentialsNames_MethodNotAllowed locks down the read-only
// contract. PATCH/POST/DELETE on this path must 405 — anything that
// could mutate the vault belongs on the admin route.
func TestCredentialsNames_MethodNotAllowed(t *testing.T) {
	srv := credentialsNamesTestServer(t)
	token, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		req := bearer(t, method, "/api/credentials/names", nil, token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s want 405, got %d", method, rec.Code)
		}
	}
}

