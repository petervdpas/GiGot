package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// TestRepoTags_PutGetRoundtrip pins the basic shape: PUT replaces
// the set, GET returns it. No inheritance on either side — this is
// the entity's *own* tag list, not the effective union.
func TestRepoTags_PutGetRoundtrip(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags", map[string]any{
		"tags": []string{"team:marketing", "env:prod"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp EntityTagsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Tags) != 2 {
		t.Fatalf("PUT response Tags = %v, want 2", resp.Tags)
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/repos/addresses/tags", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Tags) != 2 {
		t.Fatalf("GET tags = %v, want 2", resp.Tags)
	}
}

// TestRepoTags_AutoCreatesUnknownTag verifies that PUT-ing a
// previously-unknown tag name auto-creates it in the catalogue and
// emits both a tag.created (system audit) and a tag.assigned.repo
// (per-repo audit) event.
func TestRepoTags_AutoCreatesUnknownTag(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}

	beforeSys := srv.systemAudit.Count()

	rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags", map[string]any{
		"tags": []string{"brand-new-tag"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d", rec.Code)
	}

	// Catalogue now has the tag.
	listRec := do(t, srv, http.MethodGet, "/api/admin/tags", nil, sess)
	if !strings.Contains(listRec.Body.String(), "brand-new-tag") {
		t.Fatalf("auto-created tag missing from catalogue: %s", listRec.Body.String())
	}

	// System audit log gained a tag.created row.
	if got := srv.systemAudit.Count(); got != beforeSys+1 {
		t.Fatalf("system audit count: before=%d, after=%d, want +1", beforeSys, got)
	}
	events := srv.systemAudit.All()
	if events[0].Type != "tag.created" {
		t.Fatalf("newest system event = %q, want tag.created", events[0].Type)
	}
}

func TestRepoTags_RepoMustExist(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPut, "/api/admin/repos/nope/tags", map[string]any{
		"tags": []string{"x"},
	}, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestRepoTags_RequireAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags", map[string]any{
		"tags": []string{"x"},
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestRepoTags_PutDiffEmitsAuditEvents creates a known tag set, then
// replaces it, confirming the per-change audit-event count on the
// repo's audit ref matches added + removed.
func TestRepoTags_PutDiffEmitsAuditEvents(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	// Initial set.
	if rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags", map[string]any{
		"tags": []string{"a", "b"},
	}, sess); rec.Code != http.StatusOK {
		t.Fatalf("first PUT: %d", rec.Code)
	}
	auditBefore, _ := srv.git.AuditHead("addresses")

	// Replace: drop "b", add "c". Two diff events expected.
	if rec := do(t, srv, http.MethodPut, "/api/admin/repos/addresses/tags", map[string]any{
		"tags": []string{"a", "c"},
	}, sess); rec.Code != http.StatusOK {
		t.Fatalf("second PUT: %d", rec.Code)
	}
	auditAfter, _ := srv.git.AuditHead("addresses")
	if auditAfter == auditBefore {
		t.Fatal("repo audit ref did not advance after PUT diff")
	}
}

func TestAccountTags_PutGetRoundtrip(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "bob",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, http.MethodPut, "/api/admin/accounts/local/bob/tags", map[string]any{
		"tags": []string{"contractor:acme"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/accounts/local/bob/tags", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", rec.Code)
	}
	var resp EntityTagsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Tags) != 1 || resp.Tags[0] != "contractor:acme" {
		t.Fatalf("Tags = %v, want [contractor:acme]", resp.Tags)
	}
}

func TestAccountTags_AccountMustExist(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPut, "/api/admin/accounts/local/ghost/tags", map[string]any{
		"tags": []string{"x"},
	}, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestAccountTags_AssignmentEventsLandInSystemLog: account-level
// assignments are not repo-bound, so the audit events go to the
// system log, not any repo's refs/audit/main.
func TestAccountTags_AssignmentEventsLandInSystemLog(t *testing.T) {
	srv, sess := adminTestServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "bob",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	beforeSys := srv.systemAudit.Count()

	rec := do(t, srv, http.MethodPut, "/api/admin/accounts/local/bob/tags", map[string]any{
		"tags": []string{"contractor:acme"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d", rec.Code)
	}
	// Expect: tag.created (auto-create) + tag.assigned.account = +2
	if got := srv.systemAudit.Count(); got != beforeSys+2 {
		t.Fatalf("system audit: before=%d after=%d, want +2", beforeSys, got)
	}
	events := srv.systemAudit.All()
	// Newest first: assigned then created.
	if events[0].Type != "tag.assigned.account" {
		t.Errorf("events[0].Type = %q, want tag.assigned.account", events[0].Type)
	}
	if events[1].Type != "tag.created" {
		t.Errorf("events[1].Type = %q, want tag.created", events[1].Type)
	}
}
