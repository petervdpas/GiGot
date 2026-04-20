package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

func TestIssueToken(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "alice", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	payload := `{"username":"alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	var body TokenResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Username != "alice" {
		t.Errorf("expected username alice, got %s", body.Username)
	}
	if body.Token == "" {
		t.Error("expected non-empty token")
	}
}

func TestIssueTokenEmptyUsername(t *testing.T) {
	srv := testServer(t)
	payload := `{"username":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIssueTokenInvalidBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString("nope"))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestIssueToken_ScopedProvider locks down the Phase-3 scoped-username
// shape: "github:peter" resolves to (github, peter) and succeeds when
// the account exists under that provider. Bare "peter" still means
// (local, peter) for back-compat. Cross-provider collisions are
// isolated — local:alice and github:alice don't shadow each other.
func TestIssueToken_ScopedProvider(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderGitHub, Identifier: "peter", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	payload := `{"username":"github:peter"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("scoped username should succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body TokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Username != "github:peter" {
		t.Fatalf("token echoed username=%q, want scoped form preserved", body.Username)
	}
}

func TestIssueToken_ScopedRejectsUnknownProviderAccount(t *testing.T) {
	srv := testServer(t)
	// local:peter exists but github:peter does not — scoped form must
	// check the exact provider, not fall back.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "peter", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	payload := `{"username":"github:peter"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("scoped miss should 400, got %d", rec.Code)
	}
}

// TestIssueToken_RejectsUnknownAccount locks down the Phase 2 rule:
// an admin issuing a token for a username with no matching account is
// rejected outright. Phase 1's permissive auto-create is gone — callers
// must provision the account via /register or
// POST /api/admin/accounts first.
func TestIssueToken_RejectsUnknownAccount(t *testing.T) {
	srv := testServer(t)
	payload := `{"username":"newcomer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := srv.accounts.Get(accounts.ProviderLocal, "newcomer"); err == nil {
		t.Fatal("rejected issuance should not have created an account")
	}
}

func TestIssueToken_UsesExistingAccountRoleUnchanged(t *testing.T) {
	srv := testServer(t)
	// Seed alice as admin; issuing a token for alice must not demote her.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "alice",
		Role:       accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(`{"username":"alice"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	acc, _ := srv.accounts.Get(accounts.ProviderLocal, "alice")
	if acc.Role != accounts.RoleAdmin {
		t.Errorf("alice role was clobbered to %q, want admin", acc.Role)
	}
}

func TestRevokeToken(t *testing.T) {
	srv := testServer(t)

	// Issue a token first.
	token, _ := srv.tokenStrategy.Issue("bob", nil, nil)

	payload := `{"token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body MessageResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Message != "token revoked" {
		t.Errorf("expected 'token revoked', got %s", body.Message)
	}
}

func TestRevokeTokenNotFound(t *testing.T) {
	srv := testServer(t)
	payload := `{"token":"nonexistent"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRevokeTokenEmptyToken(t *testing.T) {
	srv := testServer(t)
	payload := `{"token":""}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTokenMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
