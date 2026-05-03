package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// fakeTree builds a minimal []TreeEntry with just Path populated.
// The collect* helpers under test only inspect Path; keeping the
// fixture noise low avoids accidental coupling on Size/Blob.
func fakeTree(paths ...string) []gitmanager.TreeEntry {
	out := make([]gitmanager.TreeEntry, 0, len(paths))
	for _, p := range paths {
		out = append(out, gitmanager.TreeEntry{Path: p})
	}
	return out
}

// TestRepoFormidable_EmptyRepoReturnsZeroValue — a fresh bare repo
// has no commits, no marker, no tree. The endpoint must return 200
// with the zero-value response (marker_present=false, empty
// templates and storage arrays) instead of erroring out, so a
// client connecting to a brand-new repo gets a usable response.
func TestRepoFormidable_EmptyRepoReturnsZeroValue(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/repos/addresses/formidable", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got RepoFormidableResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MarkerPresent {
		t.Fatal("empty repo should not have a marker")
	}
	if got.Marker != nil {
		t.Fatalf("marker = %+v, want nil", got.Marker)
	}
	if got.Templates == nil || len(got.Templates) != 0 {
		t.Fatalf("templates should be [] not %+v", got.Templates)
	}
	if got.Storage == nil || len(got.Storage) != 0 {
		t.Fatalf("storage should be [] not %+v", got.Storage)
	}
}

// TestRepoFormidable_OutOfScopeDenied — repo scope still gates the
// read. The bootstrap is informational, not public.
func TestRepoFormidable_OutOfScopeDenied(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "some-other-repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/repos/addresses/formidable", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoFormidable_UnknownRepoNotFound — an in-scope token still
// 404s if the repo doesn't exist on disk.
func TestRepoFormidable_UnknownRepoNotFound(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "ghost-repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodGet, "/api/repos/ghost-repo/formidable", nil, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoFormidable_NoAuthUnauthorized — auth required like every
// other repo route.
func TestRepoFormidable_NoAuthUnauthorized(t *testing.T) {
	srv := subscriberTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/addresses/formidable", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepoFormidable_NonGetRejected — keep the handler honest.
func TestRepoFormidable_NonGetRejected(t *testing.T) {
	srv := subscriberTestServer(t)
	tok, err := srv.tokenStrategy.Issue("alice", "addresses", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := bearer(t, http.MethodPost, "/api/repos/addresses/formidable",
		map[string]any{}, tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCollectFormidableTemplates_FlatYamlOnly — the helper picks up
// templates/<name>.yaml entries only, skips non-YAML and any
// nested file. Pure function; cheap to pin so the picker can't
// quietly start indexing image dirs.
func TestCollectFormidableTemplates_FlatYamlOnly(t *testing.T) {
	tree := fakeTree(
		"templates/basic.yaml",
		"templates/notes.yaml",
		"templates/images/oops.png",   // nested — skip
		"templates/README.md",         // not yaml — skip
		"storage/basic/oak.meta.json", // wrong dir — skip
	)
	got := collectFormidableTemplates(tree)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Name != "basic" || got[0].Path != "templates/basic.yaml" {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].Name != "notes" || got[1].Path != "templates/notes.yaml" {
		t.Fatalf("got[1] = %+v", got[1])
	}
}

// TestCollectFormidableStorage_GroupsByTemplate — storage entries
// roll up under their first segment with file counts. Files
// directly at storage/ root are ignored (no template name).
func TestCollectFormidableStorage_GroupsByTemplate(t *testing.T) {
	tree := fakeTree(
		"storage/addresses/oak.meta.json",
		"storage/addresses/elm.meta.json",
		"storage/addresses/images/photo.jpg",
		"storage/notes/one.meta.json",
		"storage/loose-file-no-template", // no template segment — skip
		"templates/basic.yaml",           // wrong dir — skip
	)
	got := collectFormidableStorage(tree)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	// Sorted alphabetically.
	if got[0].Template != "addresses" || got[0].Files != 3 {
		t.Fatalf("got[0] = %+v, want {addresses, 3}", got[0])
	}
	if got[1].Template != "notes" || got[1].Files != 1 {
		t.Fatalf("got[1] = %+v, want {notes, 1}", got[1])
	}
}

