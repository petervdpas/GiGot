package server

import (
	"encoding/json"
	"net/http"
	"strings"
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
			"repo":      "addresses",
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
	srv.git.InitBare("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/tokens",
		map[string]any{
			"username":  "alice",
			"repo":      "addresses",
			"abilities": []string{"notarealability"},
		}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_IssueRejectsMissingRepo locks in the one-repo-per-
// key invariant at the HTTP boundary: a body with no "repo" field is
// a 400, not a token bound to an empty string.
func TestAdminTokens_IssueRejectsMissingRepo(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/admin/tokens",
		map[string]any{"username": "alice"}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing repo, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_IssueRejectsDuplicate covers the (user, repo)
// uniqueness constraint — a second key for the same pair is a 409.
func TestAdminTokens_IssueRejectsDuplicate(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	body := map[string]any{"username": "alice", "repo": "addresses"}

	if rec := do(t, srv, http.MethodPost, "/api/admin/tokens", body, sess); rec.Code != http.StatusCreated {
		t.Fatalf("first issue want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/tokens", body, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate issue want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_PatchAddsAbility issues a token without abilities,
// then PATCHes to add "mirror" without touching repos. Proves partial
// update: pointer-to-slice semantics mean an absent field is preserved.
func TestAdminTokens_PatchAddsAbility(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
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
	// Repo must be untouched.
	if entry.Repo != "addresses" {
		t.Fatalf("PATCH abilities-only clobbered repo: %q", entry.Repo)
	}
}

// TestAdminTokens_PatchClearsAbilities proves an empty abilities array
// is distinct from "omitted": it actively clears the list.
func TestAdminTokens_PatchClearsAbilities(t *testing.T) {
	srv, sess := adminTestServer(t)

	token, err := srv.tokenStrategy.Issue("alice", "addresses", []string{"mirror"})
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

	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
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
// repos nor abilities nor tags — otherwise the admin would get a 200
// for what amounts to a no-op call, which is a confusing API contract.
func TestAdminTokens_PatchEmptyBody(t *testing.T) {
	srv, sess := adminTestServer(t)

	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": token}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for no-op patch, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminTokens_PatchSetsTagsAndListSurfacesEffective verifies the
// slice-2 contract: PATCH with tags writes them, the next list call
// returns them on the matching TokenListItem, and effective_tags on
// that row unions the sub's tags with its repo's tags.
func TestAdminTokens_PatchSetsTagsAndListSurfacesEffective(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}

	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Tag the repo so effective_tags has a non-trivial union to test.
	if rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags",
		map[string]any{"tags": []string{"team:marketing"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("repo tag PUT failed: %d", rec.Code)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token": token,
			"tags":  []string{"project:redesign"},
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH tags want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/tokens", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	var resp TokenListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var found *TokenListItem
	for i := range resp.Tokens {
		if resp.Tokens[i].Token == token {
			found = &resp.Tokens[i]
			break
		}
	}
	if found == nil {
		t.Fatal("issued token missing from list")
	}
	if len(found.Tags) != 1 || found.Tags[0] != "project:redesign" {
		t.Errorf("Tags = %v, want [project:redesign]", found.Tags)
	}
	if len(found.EffectiveTags) != 2 {
		t.Errorf("EffectiveTags = %v, want both sub + repo tags", found.EffectiveTags)
	}
}

// TestAdminTokens_PatchTagsResponseShape pins the in-place-render
// contract: when the PATCH body touches tags, the response body is
// an UpdateTokenResponse carrying the canonical post-update direct
// tags + effective_tags (sub ∪ repo ∪ account). The admin UI relies
// on this so it can patch its in-memory model without a follow-up
// listing fetch — a regression here would silently re-introduce
// the full grid re-render that resets every open abilities
// collapsible.
func TestAdminTokens_PatchTagsResponseShape(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	token, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Tag the repo so effective_tags has an inherited member.
	if rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags",
		map[string]any{"tags": []string{"team:marketing"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("repo tag PUT: %d", rec.Code)
	}

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token": token,
			"tags":  []string{"project:redesign"},
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp UpdateTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Tags) != 1 || resp.Tags[0] != "project:redesign" {
		t.Errorf("Tags = %v, want [project:redesign]", resp.Tags)
	}
	if len(resp.EffectiveTags) != 2 {
		t.Fatalf("EffectiveTags = %v, want 2 (direct + inherited)", resp.EffectiveTags)
	}
	// Order is stable (sorted by store); spot-check both members.
	hasDirect, hasInherited := false, false
	for _, n := range resp.EffectiveTags {
		if n == "project:redesign" {
			hasDirect = true
		}
		if n == "team:marketing" {
			hasInherited = true
		}
	}
	if !hasDirect || !hasInherited {
		t.Errorf("EffectiveTags missing direct or inherited: %v", resp.EffectiveTags)
	}
}

// TestAdminTokens_PatchAbilitiesOnlyKeepsMessageResponse pins the
// other side of the contract: a PATCH that doesn't touch tags
// returns the simpler MessageResponse, not the richer
// UpdateTokenResponse. Stops a future change from accidentally
// growing the response shape for every PATCH.
func TestAdminTokens_PatchAbilitiesOnlyKeepsMessageResponse(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	token, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)

	rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{
			"token":     token,
			"abilities": []string{},
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	// MessageResponse only has "message"; UpdateTokenResponse adds
	// "tags" + "effective_tags". Their absence here is the point.
	if strings.Contains(body, "\"tags\"") || strings.Contains(body, "\"effective_tags\"") {
		t.Errorf("abilities-only PATCH leaked tag fields: %s", body)
	}
	if !strings.Contains(body, "token updated") {
		t.Errorf("missing message field: %s", body)
	}
}

// TestAdminTokens_PatchTagsIdempotentNoAuditChurn pins the design Q3
// nuance: PUT-the-same-set is a no-op, so no per-assignment audit
// events fire (no diff = no event).
func TestAdminTokens_PatchTagsIdempotentNoAuditChurn(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	token, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)

	if rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": token, "tags": []string{"project:redesign"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH: %d", rec.Code)
	}
	auditBefore, _ := srv.git.AuditHead("addresses")

	if rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": token, "tags": []string{"project:redesign"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("idempotent PATCH: %d", rec.Code)
	}
	auditAfter, _ := srv.git.AuditHead("addresses")
	if auditAfter != auditBefore {
		t.Fatalf("idempotent PATCH advanced audit ref: before=%q after=%q", auditBefore, auditAfter)
	}
}
