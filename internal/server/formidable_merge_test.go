package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/formidable"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

const (
	recordPath           = "storage/addresses/oak.meta.json"
	formidableMarkerBody = `{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}
`
)

// seedFormidableRepo creates a bare repo stamped with a valid Formidable
// marker and seeds one record file. Returns the HEAD SHA so tests can
// use it as their parent_version.
func seedFormidableRepo(t *testing.T, srv *Server, repo, recordPath, recordJSON string) string {
	t.Helper()
	if err := srv.git.InitBare(repo); err != nil {
		t.Fatalf("init %s: %v", repo, err)
	}
	// Seed both files in a single commit so HEAD has the marker from
	// the very first commit (matches scaffold semantics).
	seedFile(t, srv, repo, formidableMarkerPath, formidableMarkerBody, "stamp marker")
	seedFile(t, srv, repo, recordPath, recordJSON, "seed record")
	head, err := srv.git.Head(repo)
	if err != nil {
		t.Fatalf("head %s: %v", repo, err)
	}
	return head.Version
}

// recordJSON builds a minimal meta.json body with the given updated
// timestamp and a key/value data map. Keeps test record construction
// concise.
func recordJSON(t *testing.T, updated string, data map[string]any) string {
	t.Helper()
	rec := map[string]any{
		"meta": map[string]any{
			"id":       "fixed-id",
			"template": "addresses.yaml",
			"created":  "2024-01-01T00:00:00Z",
			"updated":  updated,
		},
		"data": data,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(raw)
}

func decodeFile(t *testing.T, srv *Server, repo, path string) []byte {
	t.Helper()
	info, err := srv.git.File(repo, "", path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw, err := base64.StdEncoding.DecodeString(info.ContentB64)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return raw
}

func TestFormidableMerge_AutoMergesDisjointDataFields(t *testing.T) {
	srv := testServer(t)
	repo := "fm-auto"
	baseRec := recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"name": "Oak", "country": "nl"})
	parent := seedFormidableRepo(t, srv, repo, recordPath, baseRec)

	// Server (other client) advances: change country only.
	serverRec := recordJSON(t, "2025-02-01T00:00:00Z", map[string]any{"name": "Oak", "country": "uk"})
	seedFile(t, srv, repo, recordPath, serverRec, "server change")

	// Our client (stale parent) changes name only.
	clientRec := recordJSON(t, "2025-03-01T00:00:00Z", map[string]any{"name": "Oak Rd", "country": "nl"})
	rec := putFile(t, srv, repo, recordPath, parent, clientRec)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res gitmanager.WriteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.MergedFrom == "" || res.MergedWith == "" {
		t.Errorf("expected merge markers, got %+v", res)
	}

	// Verify the resulting record has both changes.
	raw := decodeFile(t, srv, repo, recordPath)
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	data := merged["data"].(map[string]any)
	if data["name"] != "Oak Rd" {
		t.Errorf("expected name=Oak Rd, got %v", data["name"])
	}
	if data["country"] != "uk" {
		t.Errorf("expected country=uk, got %v", data["country"])
	}
}

func TestFormidableMerge_LWWOnSameField(t *testing.T) {
	srv := testServer(t)
	repo := "fm-lww"
	base := recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"name": "Base"})
	parent := seedFormidableRepo(t, srv, repo, recordPath, base)

	// Server change: name=Theirs at 2025-02-01.
	serverRec := recordJSON(t, "2025-02-01T00:00:00Z", map[string]any{"name": "Theirs"})
	seedFile(t, srv, repo, recordPath, serverRec, "server change")

	// Client change: name=Yours at 2025-03-01 (newer).
	clientRec := recordJSON(t, "2025-03-01T00:00:00Z", map[string]any{"name": "Yours"})
	rec := putFile(t, srv, repo, recordPath, parent, clientRec)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	raw := decodeFile(t, srv, repo, recordPath)
	var merged map[string]any
	json.Unmarshal(raw, &merged)
	data := merged["data"].(map[string]any)
	if data["name"] != "Yours" {
		t.Errorf("LWW: expected yours to win (newer updated), got %v", data["name"])
	}
}

// TestFormidableMerge_ImmutableMetaSilentlyTakesTheirs — when the
// client tries to mutate an immutable meta key (created/id/template),
// the server silently reconciles to HEAD's content instead of
// returning 409. The client's write becomes a no-op at the content
// level — the server-authored merge commit's tree equals HEAD's tree.
// (Formerly TestFormidableMerge_ImmutableMetaReturns409 — the 409
// path was removed when the take-theirs policy landed.)
func TestFormidableMerge_ImmutableMetaSilentlyTakesTheirs(t *testing.T) {
	srv := testServer(t)
	repo := "fm-imm"
	base := recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"name": "Oak"})
	parent := seedFormidableRepo(t, srv, repo, recordPath, base)

	// Server change with the canonical created — establishes HEAD's
	// version of the record.
	serverContent := recordJSON(t, "2025-02-01T00:00:00Z", map[string]any{"name": "Oak"})
	seedFile(t, srv, repo, recordPath, serverContent, "server noop-ish")

	// Client tries to rewrite meta.created — illegal under the
	// immutable-meta rule.
	badMeta := map[string]any{
		"meta": map[string]any{
			"id":       "fixed-id",
			"template": "addresses.yaml",
			"created":  "1999-01-01T00:00:00Z", // changed!
			"updated":  "2025-03-01T00:00:00Z",
		},
		"data": map[string]any{"name": "Oak"},
	}
	raw, _ := json.Marshal(badMeta)
	rec := putFile(t, srv, repo, recordPath, parent, string(raw))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (silent take-theirs), got %d: %s", rec.Code, rec.Body.String())
	}

	// Body should NOT be a RecordConflict — that path is dead now.
	var conflictProbe formidable.RecordConflict
	if err := json.Unmarshal(rec.Body.Bytes(), &conflictProbe); err == nil && len(conflictProbe.FieldConflicts) > 0 {
		t.Errorf("response unexpectedly carries RecordConflict body: %+v", conflictProbe)
	}

	// Read back: the on-disk record at HEAD must match the SERVER's
	// content, not the client's bad meta.
	got := decodeFile(t, srv, repo, recordPath)
	if string(got) != serverContent {
		t.Errorf("expected HEAD blob == server content (silent take-theirs), got %s", string(got))
	}
}

func TestFormidableMerge_SkipsWhenMarkerAbsent(t *testing.T) {
	srv := testServer(t)
	// Seed a bare repo without the marker; just the record file.
	parent := seedBareWith(t, srv, "fm-nomark", recordPath,
		recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"name": "Oak"}))

	// Server edits the record.
	seedFile(t, srv, "fm-nomark", recordPath,
		recordJSON(t, "2025-02-01T00:00:00Z", map[string]any{"name": "Oak", "country": "nl"}),
		"server change")

	// Client tries a change that would cleanly merge under formidable
	// rules but will fail line-based merge because JSON isn't
	// canonical. We only assert we do NOT get a RecordConflict shape
	// (formidable path was skipped).
	clientRec := recordJSON(t, "2025-03-01T00:00:00Z", map[string]any{"name": "Oak Rd"})
	rec := putFile(t, srv, "fm-nomark", recordPath, parent, clientRec)
	if rec.Code == http.StatusConflict {
		// If it did conflict, the body shape must not be RecordConflict.
		var body formidable.RecordConflict
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil && body.FieldConflicts != nil && len(body.FieldConflicts) > 0 {
			t.Errorf("non-Formidable repo should not produce RecordConflict body, got %+v", body)
		}
	}
}

func TestFormidableMerge_RejectsInvalidRecordPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"storage/addresses/oak.meta.json", true},
		{"storage/addresses/images/foo.png", false}, // images/ guard
		{"storage/addresses/sub/x.meta.json", false}, // too deep
		{"storage/oak.meta.json", false},             // too shallow
		{"templates/x.yaml", false},
		{"../storage/a/x.meta.json", false},
		{"/storage/a/x.meta.json", false},
		{"storage/addresses/oak.json", false}, // missing .meta suffix
	}
	for _, c := range cases {
		got := isFormidableRecordPath(c.path)
		if got != c.want {
			t.Errorf("isFormidableRecordPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestFormidableMerge_CommitSilentlyTakesTheirsOnImmutableMeta — when
// a multi-commit POST contains a record-path change that violates an
// immutable meta key, the handler silently substitutes HEAD's content
// for that path before committing. The commit succeeds; the on-disk
// blob equals HEAD's pre-commit content. No 409.
// (Formerly TestFormidableMerge_CommitAggregatesImmutableConflicts —
// the conflict-aggregation path was removed when the take-theirs
// policy landed.)
func TestFormidableMerge_CommitSilentlyTakesTheirsOnImmutableMeta(t *testing.T) {
	srv := testServer(t)
	repo := "fm-commit"
	base := recordJSON(t, "2025-01-01T00:00:00Z", map[string]any{"name": "Oak"})
	parent := seedFormidableRepo(t, srv, repo, recordPath, base)

	// Server edits record — establishes HEAD's view of this record.
	serverContent := recordJSON(t, "2025-02-01T00:00:00Z", map[string]any{"name": "Oak"})
	seedFile(t, srv, repo, recordPath, serverContent, "s")

	// Commit that tries to change meta.created on the same record.
	badRec := map[string]any{
		"meta": map[string]any{
			"id":       "fixed-id",
			"template": "addresses.yaml",
			"created":  "1999-01-01T00:00:00Z",
			"updated":  "2025-03-01T00:00:00Z",
		},
		"data": map[string]any{"name": "Oak"},
	}
	rawBad, _ := json.Marshal(badRec)
	body := map[string]any{
		"parent_version": parent,
		"changes": []map[string]any{
			{
				"op":          "put",
				"path":        recordPath,
				"content_b64": base64.StdEncoding.EncodeToString(rawBad),
			},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/repos/"+repo+"/commits", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (silent take-theirs), got %d: %s", rec.Code, rec.Body.String())
	}

	// Body must NOT carry a CommitRecordConflictResponse — that path
	// is dead now.
	var conflictProbe CommitRecordConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &conflictProbe); err == nil && len(conflictProbe.RecordConflicts) > 0 {
		t.Errorf("response unexpectedly carries RecordConflicts: %+v", conflictProbe.RecordConflicts)
	}

	// Read back: the on-disk record at HEAD must match the SERVER's
	// content, not the client's bad meta.
	got := decodeFile(t, srv, repo, recordPath)
	if string(got) != serverContent {
		t.Errorf("expected HEAD blob == server content (silent take-theirs), got %s", string(got))
	}
}
