package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// TestBindToken_CreatesRegularAccountForLegacyUsername covers the
// Phase 2 /api/admin/tokens/bind action: a token minted before the
// accounts model existed still points at a free-text username. The
// bind action creates the matching regular account so the token stops
// being a dangling legacy row.
func TestBindToken_CreatesRegularAccountForLegacyUsername(t *testing.T) {
	srv, sess := adminTestServer(t)
	// Bypass the handler (which would reject the unknown account) and
	// drop a legacy token directly on the strategy — this models the
	// pre-Phase-1 boot state.
	tok, err := srv.tokenStrategy.Issue("legacy-user", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.accounts.Has(accounts.ProviderLocal, "legacy-user") {
		t.Fatal("precondition: legacy-user should have no account")
	}

	rec := do(t, srv, http.MethodPost, "/api/admin/tokens/bind",
		map[string]any{"token": tok}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Identifier != "legacy-user" || view.Role != accounts.RoleRegular {
		t.Fatalf("wrong account returned: %+v", view)
	}
	if !srv.accounts.Has(accounts.ProviderLocal, "legacy-user") {
		t.Fatal("account was not persisted after bind")
	}
}

func TestBindToken_IdempotentWhenAlreadyBound(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "already-bound", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := srv.tokenStrategy.Issue("already-bound", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/tokens/bind",
		map[string]any{"token": tok}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent call should 200, got %d", rec.Code)
	}
}

func TestBindToken_404OnUnknownToken(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/admin/tokens/bind",
		map[string]any{"token": "nonexistent-token"}, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestListTokens_HasAccountFlag covers the UI-facing signal that
// distinguishes legacy tokens from accounts-bound ones in the token
// list response.
func TestListTokens_HasAccountFlag(t *testing.T) {
	srv, sess := adminTestServer(t)

	// Bound: create the account first, then issue.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bound-user", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("bound-user", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Unbound (legacy-shaped): issue without an account.
	if _, err := srv.tokenStrategy.Issue("orphan-user", nil, nil); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodGet, "/api/admin/tokens", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var list TokenListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)

	got := map[string]bool{}
	for _, tok := range list.Tokens {
		got[tok.Username] = tok.HasAccount
	}
	if !got["bound-user"] {
		t.Error("bound-user should have HasAccount=true")
	}
	if got["orphan-user"] {
		t.Error("orphan-user should have HasAccount=false")
	}
}
