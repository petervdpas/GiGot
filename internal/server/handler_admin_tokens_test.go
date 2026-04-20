package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAdminTokens_IssueWithAbilities proves the POST path persists the
// abilities field end-to-end: the response echoes it, and the subsequent
// GET listing still shows it. Positive half of the abilities pair.
func TestAdminTokens_IssueWithAbilities(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/tokens",
		map[string]any{
			"username":  "alice",
			"repos":     []string{"addresses"},
			"abilities": []string{"mirror"},
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Abilities) != 1 || got.Abilities[0] != "mirror" {
		t.Fatalf("POST response missing abilities: %+v", got)
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/tokens", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}
	var list TokenListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || len(list.Tokens[0].Abilities) != 1 ||
		list.Tokens[0].Abilities[0] != "mirror" {
		t.Fatalf("GET listing missing abilities: %+v", list)
	}
}

// TestAdminTokens_IssueRejectsUnknownAbility is the negative half: a
// typo or future-ability name at issue time is a 400, not a silently
// persisted no-op.
func TestAdminTokens_IssueRejectsUnknownAbility(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/tokens",
		map[string]any{
			"username":  "alice",
			"abilities": []string{"notarealability"},
		}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_PatchAddsAbility issues a token without abilities,
// then PATCHes to add "mirror" without touching repos. Proves partial
// update: pointer-to-slice semantics mean an absent field is preserved.
func TestAdminTokens_PatchAddsAbility(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	token, err := srv.tokenStrategy.Issue("alice", []string{"addresses"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token":     token,
			"abilities": []string{"mirror"},
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	entry := srv.tokenStrategy.Get(token)
	if entry == nil {
		t.Fatal("token disappeared after PATCH")
	}
	if !entry.HasAbility("mirror") {
		t.Fatalf("expected mirror ability, got %+v", entry.Abilities)
	}
	// Repos must be untouched.
	if len(entry.Repos) != 1 || entry.Repos[0] != "addresses" {
		t.Fatalf("PATCH abilities-only clobbered repos: %+v", entry.Repos)
	}
}

// TestAdminTokens_PatchClearsAbilities proves an empty abilities array
// is distinct from "omitted": it actively clears the list.
func TestAdminTokens_PatchClearsAbilities(t *testing.T) {
	srv, sess := adminTestServer(t)

	token, err := srv.tokenStrategy.Issue("alice", nil, []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token":     token,
			"abilities": []string{},
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	entry := srv.tokenStrategy.Get(token)
	if entry.HasAbility("mirror") {
		t.Fatalf("mirror should be cleared, got %+v", entry.Abilities)
	}
}

// TestAdminTokens_PatchRejectsUnknownAbility mirrors the POST-side
// negative: an unknown ability name is 400 on PATCH too.
func TestAdminTokens_PatchRejectsUnknownAbility(t *testing.T) {
	srv, sess := adminTestServer(t)

	token, err := srv.tokenStrategy.Issue("alice", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token":     token,
			"abilities": []string{"bogus"},
		}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_PatchEmptyBody rejects a PATCH that specifies neither
// repos nor abilities — otherwise the admin would get a 200 for what
// amounts to a no-op call, which is a confusing API contract.
func TestAdminTokens_PatchEmptyBody(t *testing.T) {
	srv, sess := adminTestServer(t)

	token, err := srv.tokenStrategy.Issue("alice", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": token}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for no-op patch, got %d body=%s", rec.Code, rec.Body.String())
	}
}
