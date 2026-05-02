package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestTags_RequireAdminSession is the gate every other tag test
// implicitly relies on — without it, a session-less call could be
// silently writing to the catalogue and the rest of the suite would
// still pass.
func TestTags_RequireAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/tags", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without session, got %d", rec.Code)
	}
}

// TestTags_CreateAndList round-trips a tag through POST + GET and
// asserts the wire shape (id, name, created_at, usage block) so a
// future schema change breaks here, not at the UI layer.
func TestTags_CreateAndList(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:marketing"}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created TagView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created tag missing id")
	}
	if created.Name != "team:marketing" {
		t.Fatalf("name = %q, want team:marketing", created.Name)
	}
	if created.Usage.Repos != 0 || created.Usage.Subscriptions != 0 || created.Usage.Accounts != 0 {
		t.Fatalf("fresh tag has non-zero usage: %+v", created.Usage)
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/tags", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	var listed TagListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Count != 1 || len(listed.Tags) != 1 {
		t.Fatalf("count = %d, len = %d, want both 1", listed.Count, len(listed.Tags))
	}
	if listed.Tags[0].ID != created.ID {
		t.Fatalf("listed id = %q, want %q", listed.Tags[0].ID, created.ID)
	}
}

// TestTags_CreateDuplicateRejects pins the case-insensitive
// uniqueness contract: Team:Marketing and team:marketing collide.
func TestTags_CreateDuplicateRejects(t *testing.T) {
	srv, sess := adminTestServer(t)

	if rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "Team:Marketing"}, sess); rec.Code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d", rec.Code)
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:marketing"}, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTags_CreateRejectsForbiddenChars pins §8: tag names cannot
// carry path-segment-breaking characters.
func TestTags_CreateRejectsForbiddenChars(t *testing.T) {
	srv, sess := adminTestServer(t)
	for _, bad := range []string{"team/marketing", "team?x", "team#y"} {
		rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": bad}, sess)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: want 400, got %d", bad, rec.Code)
		}
	}
}

// TestTags_RenameHappyPath checks the rename round-trip and that the
// old name disappears from the index (a re-create under the old name
// must succeed).
func TestTags_RenameHappyPath(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:mktg"}, sess)
	var created TagView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = do(t, srv, http.MethodPatch, "/api/admin/tags/"+created.ID, map[string]any{"name": "team:marketing"}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var renamed TagView
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "team:marketing" {
		t.Fatalf("renamed.Name = %q, want team:marketing", renamed.Name)
	}

	// Re-create under the old name must now succeed (index entry
	// freed by the rename).
	rec = do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:mktg"}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("recreate after rename: want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTags_RenameCollisionRejects: rename targeting an existing
// (different) tag's name is a 409.
func TestTags_RenameCollisionRejects(t *testing.T) {
	srv, sess := adminTestServer(t)

	_ = do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:marketing"}, sess)
	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "team:platform"}, sess)
	var platform TagView
	_ = json.Unmarshal(rec.Body.Bytes(), &platform)

	rec = do(t, srv, http.MethodPatch, "/api/admin/tags/"+platform.ID, map[string]any{"name": "TEAM:marketing"}, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename collide: want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTags_DeleteReturnsSweepCounts pins the response shape from §6.1:
// DELETE returns {deleted, swept} so the audit log and confirm dialog
// see the blast radius. Slice 1 has no assignments, so all sweep
// counts are zero — but the wire shape is still tested here.
func TestTags_DeleteReturnsSweepCounts(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "contractor:acme"}, sess)
	var created TagView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = do(t, srv, http.MethodDelete, "/api/admin/tags/"+created.ID, nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp TagDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Deleted.ID != created.ID {
		t.Fatalf("deleted.id = %q, want %q", resp.Deleted.ID, created.ID)
	}
	if resp.Swept.Repos != 0 || resp.Swept.Subscriptions != 0 || resp.Swept.Accounts != 0 {
		t.Fatalf("unexpected sweep counts: %+v", resp.Swept)
	}

	// The tag is actually gone — re-create succeeds.
	rec = do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "contractor:acme"}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-create after delete: want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTags_DeleteUnknownReturns404 — destructive ops on a missing
// resource must fail loud, not silently no-op.
func TestTags_DeleteUnknownReturns404(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodDelete, "/api/admin/tags/nope-not-a-real-id", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestTags_AuditEventsLanded covers Q3 of the design checklist: every
// catalogue lifecycle action emits a system-audit event. Slice 1 only
// covers tag.created / tag.renamed / tag.deleted; assignment events
// land in slice 2.
func TestTags_AuditEventsLanded(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/tags", map[string]any{"name": "env:prod"}, sess)
	var created TagView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	_ = do(t, srv, http.MethodPatch, "/api/admin/tags/"+created.ID, map[string]any{"name": "env:production"}, sess)
	_ = do(t, srv, http.MethodDelete, "/api/admin/tags/"+created.ID, nil, sess)

	events := srv.systemAudit.All()
	// Newest-first; expect deleted, renamed, created in that order.
	if len(events) != 3 {
		t.Fatalf("want 3 audit events, got %d: %+v", len(events), events)
	}
	wantTypes := []string{"tag.deleted", "tag.renamed", "tag.created"}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	// The renamed event payload must carry both the old and the new
	// name — that's the load-bearing forensic information for a
	// future "what did team:foo used to mean?" query.
	if !strings.Contains(string(events[1].Payload), "env:prod") || !strings.Contains(string(events[1].Payload), "env:production") {
		t.Errorf("rename payload missing old or new name: %s", events[1].Payload)
	}
	// Actor on every event is the calling admin's username.
	for i, e := range events {
		if e.Actor.Username != "alice" {
			t.Errorf("events[%d].Actor.Username = %q, want alice", i, e.Actor.Username)
		}
	}
}
