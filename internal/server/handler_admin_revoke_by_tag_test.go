package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestRevokeByTag_HappyPath issues two subscriptions, tags one with
// `team:marketing`, calls the bulk-revoke endpoint with the typed
// confirm phrase, and verifies only the tagged sub is gone afterwards.
// Pins the AND-on-effective-tags rule + the typed-phrase gate together.
func TestRevokeByTag_HappyPath(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}

	keepTok, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	revokeTok, err := srv.tokenStrategy.Issue("bob", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Tag bob's sub directly so the AND-set match has work to do.
	if rec := do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": revokeTok, "tags": []string{"team:marketing"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("seed PATCH: %d body=%s", rec.Code, rec.Body.String())
	}

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{"team:marketing"},
			"confirm": "revoke team:marketing",
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke-by-tag want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp RevokeByTagResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 1 || len(resp.Revoked) != 1 || resp.Revoked[0].Token != revokeTok {
		t.Fatalf("expected only %s revoked, got %+v", revokeTok, resp)
	}
	if srv.tokenStrategy.Get(revokeTok) != nil {
		t.Errorf("revoked token still present in store")
	}
	if srv.tokenStrategy.Get(keepTok) == nil {
		t.Errorf("untagged token swept by mistake")
	}
}

// TestRevokeByTag_RejectsMissingConfirm pins the typed-phrase gate as
// load-bearing: without confirm we 400, the matching sub stays alive.
// The phrase being deterministic from the request is the contract that
// makes the UI affordance copy-pasteable.
func TestRevokeByTag_RejectsMissingConfirm(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	tok, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": tok, "tags": []string{"team:marketing"}}, sess)

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{"tags": []string{"team:marketing"}}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing confirm, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.tokenStrategy.Get(tok) == nil {
		t.Fatalf("missing-confirm 400 still revoked the token")
	}
}

// TestRevokeByTag_RejectsWrongConfirm pins the gate against typo /
// stale phrase: a different phrase is rejected even though the tag
// list is well-formed.
func TestRevokeByTag_RejectsWrongConfirm(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	tok, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": tok, "tags": []string{"team:marketing"}}, sess)

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{"team:marketing"},
			"confirm": "revoke",
		}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for wrong confirm, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.tokenStrategy.Get(tok) == nil {
		t.Fatalf("wrong-confirm 400 still revoked the token")
	}
}

// TestRevokeByTag_RejectsEmptyTags pins the §5.6 invariant: a
// bulk-revoke with no tag filter would clear the catalogue, which is
// banned at the API boundary regardless of confirm phrase.
func TestRevokeByTag_RejectsEmptyTags(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	tok, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{},
			"confirm": "revoke ",
		}, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for empty tag list, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.tokenStrategy.Get(tok) == nil {
		t.Fatalf("empty-tags 400 still revoked the token")
	}
}

// TestRevokeByTag_AndSemantics two tags must both be present (effective
// AND) for a sub to be in scope. A sub with one of the two tags
// survives.
func TestRevokeByTag_AndSemantics(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	bothTok, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	oneTok, _ := srv.tokenStrategy.Issue("bob", "addresses", nil)

	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": bothTok, "tags": []string{"team:marketing", "env:prod"}}, sess)
	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": oneTok, "tags": []string{"team:marketing"}}, sess)

	// Tags in confirm phrase must be sorted lower-case.
	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{"env:prod", "team:marketing"},
			"confirm": "revoke env:prod,team:marketing",
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp RevokeByTagResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 1 || resp.Revoked[0].Token != bothTok {
		t.Fatalf("expected only %s revoked (both tags), got %+v", bothTok, resp)
	}
	if srv.tokenStrategy.Get(oneTok) == nil {
		t.Errorf("partial-match token swept incorrectly")
	}
}

// TestRevokeByTag_MatchesInheritedTags pins the powerful + dangerous
// case from §7: an account-tagged contractor's keys are revocable by
// tag even if the keys themselves carry no direct tag. This is the
// load-bearing contractor-lifecycle workflow from §1.
func TestRevokeByTag_MatchesInheritedTags(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	// Create a regular account so the (provider, identifier) key
	// resolves and account tags can attach.
	do(t, srv, http.MethodPost, "/api/admin/accounts",
		map[string]any{"provider": "local", "identifier": "bob", "role": "regular"}, sess)
	tok, _ := srv.tokenStrategy.Issue("local:bob", "addresses", nil)

	// Tag the *account*, not the sub.
	if rec := do(t, srv, http.MethodPut, "/api/admin/accounts/local/bob/tags",
		map[string]any{"tags": []string{"contractor:acme"}}, sess); rec.Code != http.StatusOK {
		t.Fatalf("seed account tag PUT: %d", rec.Code)
	}

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{"contractor:acme"},
			"confirm": "revoke contractor:acme",
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.tokenStrategy.Get(tok) != nil {
		t.Errorf("inherited-tag match did not revoke the contractor's key")
	}
}

// TestRevokeByTag_TagListFilter pins the GET ?tag= filter as the
// counterpart to the bulk action. Two tokens, only one with the tag,
// listing with ?tag=team:marketing returns only the tagged one. AND
// across multiple ?tag= params shrinks further.
func TestRevokeByTag_TagListFilter(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	tagged, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	srv.tokenStrategy.Issue("bob", "addresses", nil)
	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": tagged, "tags": []string{"team:marketing"}}, sess)

	rec := do(t, srv, http.MethodGet, "/api/admin/tokens?tag=team:marketing", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp TokenListResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 1 || resp.Tokens[0].Token != tagged {
		t.Fatalf("filter returned %+v, want only %s", resp.Tokens, tagged)
	}

	// AND with a tag nothing carries → empty.
	rec = do(t, srv, http.MethodGet, "/api/admin/tokens?tag=team:marketing&tag=missing", nil, sess)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("AND with non-existent tag should be empty, got %d", resp.Count)
	}
}

// TestRevokeByTag_RequiresAdminSession pins the auth fence: no cookie
// is a 401 before the body is even parsed.
func TestRevokeByTag_RequiresAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{"tags": []string{"x"}, "confirm": "revoke x"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without session, got %d", rec.Code)
	}
}

// TestRevokeByTag_AuditEventOnRepo records that a bulk revoke leaves a
// tag.revoked.bulk event on the repo's audit chain. We can't easily
// decode the audit notes here, but the chain head must advance.
func TestRevokeByTag_AuditEventOnRepo(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")
	tok, _ := srv.tokenStrategy.Issue("alice", "addresses", nil)
	do(t, srv, http.MethodPatch, "/api/admin/tokens",
		map[string]any{"token": tok, "tags": []string{"team:marketing"}}, sess)
	before, _ := srv.git.AuditHead("addresses")

	rec := do(t, srv, http.MethodPost, "/api/admin/subscriptions/revoke-by-tag",
		map[string]any{
			"tags":    []string{"team:marketing"},
			"confirm": "revoke team:marketing",
		}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	after, _ := srv.git.AuditHead("addresses")
	if after == "" || after == before {
		t.Errorf("audit chain did not advance: before=%q after=%q", before, after)
	}
}
