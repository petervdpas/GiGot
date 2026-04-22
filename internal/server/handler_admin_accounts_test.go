package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

func TestAdminAccounts_RequireSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/accounts", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAdminAccounts_ListIncludesSeededAdmin(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/accounts", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var list AccountListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count == 0 {
		t.Fatal("expected at least the admin seed, got empty list")
	}
	// Find alice — seeded by adminTestServer.
	var alice *AccountView
	for i := range list.Accounts {
		if list.Accounts[i].Identifier == "alice" {
			alice = &list.Accounts[i]
			break
		}
	}
	if alice == nil {
		t.Fatalf("alice not in list: %+v", list.Accounts)
	}
	if alice.Role != accounts.RoleAdmin {
		t.Errorf("alice role=%q, want admin", alice.Role)
	}
	if !alice.HasPassword {
		t.Errorf("alice should show HasPassword=true; adminTestServer sets one")
	}
}

func TestAdminAccounts_CreateRegular(t *testing.T) {
	srv, sess := adminTestServer(t)
	body := map[string]any{
		"provider":     "local",
		"identifier":   "bob",
		"role":         "regular",
		"display_name": "Bob",
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/accounts", body, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Role != accounts.RoleRegular || view.Identifier != "bob" {
		t.Fatalf("bad view: %+v", view)
	}
	if view.HasPassword {
		t.Error("bob was created without a password; HasPassword must be false")
	}
}

func TestAdminAccounts_CreateWithPassword(t *testing.T) {
	srv, sess := adminTestServer(t)
	body := map[string]any{
		"provider":   "local",
		"identifier": "carol",
		"role":       "regular",
		"password":   "pw123456",
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/accounts", body, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if !view.HasPassword {
		t.Fatal("HasPassword should be true after create-with-password")
	}
	// Login must succeed with the same password so we know it landed on
	// the same account, not silently stored on a different row.
	if _, err := srv.accounts.Verify("carol", "pw123456"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestAdminAccounts_CreateRejectsDuplicate(t *testing.T) {
	srv, sess := adminTestServer(t)
	body := map[string]any{"provider": "local", "identifier": "alice", "role": "admin"}
	rec := do(t, srv, http.MethodPost, "/api/admin/accounts", body, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminAccounts_CreateRejectsBadRole(t *testing.T) {
	srv, sess := adminTestServer(t)
	body := map[string]any{"provider": "local", "identifier": "dana", "role": "viewer"}
	rec := do(t, srv, http.MethodPost, "/api/admin/accounts", body, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad role, got %d", rec.Code)
	}
}

func TestAdminAccounts_PatchRole(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	// Promote bob to admin.
	newRole := "admin"
	rec := do(t, srv, http.MethodPatch, "/api/admin/accounts/local/bob",
		map[string]any{"role": newRole}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Role != accounts.RoleAdmin {
		t.Errorf("role=%q, want admin", view.Role)
	}
}

func TestAdminAccounts_PatchRefusesDemotingLastAdmin(t *testing.T) {
	srv, sess := adminTestServer(t)
	// adminTestServer seeds alice as admin on top of the config-default
	// "admin" seed — remove the default so alice is the only admin,
	// otherwise this test is proving something else.
	_ = srv.accounts.Remove(accounts.ProviderLocal, "admin")

	rec := do(t, srv, http.MethodPatch, "/api/admin/accounts/local/alice",
		map[string]any{"role": "regular"}, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 for last-admin demotion, got %d body=%s", rec.Code, rec.Body.String())
	}
	acc, _ := srv.accounts.Get(accounts.ProviderLocal, "alice")
	if acc.Role != accounts.RoleAdmin {
		t.Fatal("alice was demoted despite 409 response")
	}
}

func TestAdminAccounts_PatchSetsPassword(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	pw := "new-pw-value"
	rec := do(t, srv, http.MethodPatch, "/api/admin/accounts/local/bob",
		map[string]any{"password": pw}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := srv.accounts.Verify("bob", pw); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestAdminAccounts_Delete(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodDelete, "/api/admin/accounts/local/bob", nil, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.accounts.Has(accounts.ProviderLocal, "bob") {
		t.Fatal("bob should be gone after DELETE")
	}
}

// TestAdminAccounts_SubscriptionCount covers the count the Accounts
// page uses to render its "Subscriptions" column. Scoped tokens
// (github:peter) and the legacy bare shorthand (local shorthand
// "alice") both land on their matching account — counts come out
// right for both forms, and admins with no subs show 0.
func TestAdminAccounts_SubscriptionCount(t *testing.T) {
	srv, sess := adminTestServer(t)
	// Two accounts to count against.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "bob", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderGitHub, Identifier: "peter", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	// Three tokens: two for bob (scoped via local:bob and legacy bare
	// "bob") and one for peter. Each key is bound to a distinct repo
	// so the "one-key-per-(user, repo)" invariant holds.
	if _, err := srv.tokenStrategy.Issue("local:bob", "repo-a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("bob", "repo-b", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tokenStrategy.Issue("github:peter", "repo-a", nil); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodGet, "/api/admin/accounts", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var list AccountListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)

	counts := map[string]int{}
	for _, a := range list.Accounts {
		counts[a.Provider+":"+a.Identifier] = a.SubscriptionCount
	}
	if counts["local:bob"] != 2 {
		t.Errorf("local:bob sub count = %d, want 2 (scoped + legacy)", counts["local:bob"])
	}
	if counts["github:peter"] != 1 {
		t.Errorf("github:peter sub count = %d, want 1", counts["github:peter"])
	}
	// alice (seeded by adminTestServer) has no tokens; must report 0.
	if counts["local:alice"] != 0 {
		t.Errorf("local:alice sub count = %d, want 0", counts["local:alice"])
	}
}

func TestAdminAccounts_DeleteRefusesLastAdmin(t *testing.T) {
	srv, sess := adminTestServer(t)
	_ = srv.accounts.Remove(accounts.ProviderLocal, "admin")

	rec := do(t, srv, http.MethodDelete, "/api/admin/accounts/local/alice", nil, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 for last-admin delete, got %d", rec.Code)
	}
	if !srv.accounts.Has(accounts.ProviderLocal, "alice") {
		t.Fatal("alice was deleted despite 409")
	}
}
