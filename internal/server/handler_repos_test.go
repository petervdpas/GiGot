package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// countCommits returns the number of commits reachable from HEAD in the
// given bare repo path, or 0 if the repo has no commits yet. Used by the
// stamping matrix to assert "stamp wrote exactly one commit" invariants.
func countCommits(t *testing.T, repoPath string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		// No HEAD yet (empty repo) — rev-list exits non-zero.
		return 0
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		if c < '0' || c > '9' {
			t.Fatalf("unexpected rev-list output: %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// seedCloneSource creates a tiny non-bare git repo with one commit and returns
// its path, suitable as a source_url for the create-repo handler.
func seedCloneSource(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "source")
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	cmds := [][]string{
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "add", "README.md"},
		{"-C", dir, "commit", "-m", "initial"},
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

func TestListReposEmpty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoListResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Count != 0 {
		t.Errorf("expected 0 repos, got %d", body.Count)
	}
}

func TestListReposWithEntries(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("alpha")
	srv.git.InitBare("beta")

	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	var body RepoListResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Count != 2 {
		t.Errorf("expected 2 repos, got %d", body.Count)
	}
}

func TestCreateRepo(t *testing.T) {
	srv := testServer(t)
	payload := `{"name":"new-project"}`
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	if !srv.git.Exists("new-project") {
		t.Error("repo should exist after creation")
	}
}

func TestCreateRepoEmptyName(t *testing.T) {
	srv := testServer(t)
	payload := `{"name":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateRepoDuplicate(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("existing")

	payload := `{"name":"existing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rec.Code)
	}
}

func TestCreateRepoInvalidBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGetRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("my-repo")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/my-repo", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoInfo
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Name != "my-repo" {
		t.Errorf("expected name my-repo, got %s", body.Name)
	}
}

func TestRepoInfo_EnrichedFields(t *testing.T) {
	// This test pins down that the admin UI's card data — empty flag,
	// HEAD, default branch, Formidable marker presence, destination
	// count — all come from the list endpoint. Breaking any of these
	// will make a card render stale or empty data.
	srv, sess := adminTestServer(t)

	// Empty repo: Empty=true, no HEAD, no branch, no marker, zero dests.
	if err := srv.git.InitBare("fresh"); err != nil {
		t.Fatal(err)
	}

	// Scaffolded repo: HasFormidable=true, HEAD set, branch populated.
	if _, err := srv.createRepoForTest("scaffolded", true); err != nil {
		t.Fatalf("create scaffolded: %v", err)
	}

	// Attach a destination to the scaffolded repo so DestinationCount=1.
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/scaffolded/destinations",
		map[string]any{
			"url":             "https://example.org/mirror.git",
			"credential_name": "github-personal",
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("destination seed failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, http.MethodGet, "/api/repos", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d", rec.Code)
	}
	var list RepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byName := map[string]RepoInfo{}
	for _, r := range list.Repos {
		byName[r.Name] = r
	}

	fresh, ok := byName["fresh"]
	if !ok {
		t.Fatal("fresh repo missing from list")
	}
	if !fresh.Empty {
		t.Errorf("fresh.Empty = false, want true")
	}
	if fresh.Head != "" || fresh.DefaultBranch != "" {
		t.Errorf("fresh repo should have no head fields, got %+v", fresh)
	}
	if fresh.HasFormidable {
		t.Errorf("fresh.HasFormidable = true, want false")
	}
	if fresh.DestinationCount != 0 {
		t.Errorf("fresh.DestinationCount = %d, want 0", fresh.DestinationCount)
	}
	if fresh.Commits != 0 {
		t.Errorf("fresh.Commits = %d, want 0", fresh.Commits)
	}

	scaf, ok := byName["scaffolded"]
	if !ok {
		t.Fatal("scaffolded repo missing from list")
	}
	if scaf.Empty {
		t.Error("scaffolded.Empty = true, want false")
	}
	if len(scaf.Head) < 7 {
		t.Errorf("scaffolded.Head looks wrong: %q", scaf.Head)
	}
	if scaf.DefaultBranch == "" {
		t.Error("scaffolded.DefaultBranch empty")
	}
	if !scaf.HasFormidable {
		t.Error("scaffolded.HasFormidable = false, want true")
	}
	if scaf.DestinationCount != 1 {
		t.Errorf("scaffolded.DestinationCount = %d, want 1", scaf.DestinationCount)
	}
	if scaf.Commits != 1 {
		// Scaffold writes exactly one commit (the initial seeded tree).
		t.Errorf("scaffolded.Commits = %d, want 1", scaf.Commits)
	}
}

// createRepoForTest is a small helper that drives POST /api/repos so
// test setup can reuse the real creation pipeline (scaffold, marker
// stamp) instead of bypassing it with InitBare.
func (s *Server) createRepoForTest(name string, scaffold bool) (*http.Response, error) {
	body := map[string]any{"name": name, "scaffold_formidable": scaffold}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBuffer(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestGetRepoNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("doomed")

	req := httptest.NewRequest(http.MethodDelete, "/api/repos/doomed", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}

	if srv.git.Exists("doomed") {
		t.Error("repo should not exist after deletion")
	}
}

func TestDeleteRepoNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/repos/ghost", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCreateRepoClone(t *testing.T) {
	srv := testServer(t)
	source := seedCloneSource(t)

	payload, _ := json.Marshal(map[string]any{
		"name":       "cloned",
		"source_url": source,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !srv.git.Exists("cloned") {
		t.Fatal("repo should exist after clone")
	}

	out, err := exec.Command("git", "-C", srv.git.RepoPath("cloned"), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("cloned repo should have HEAD: %v", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Fatal("cloned repo HEAD should be non-empty")
	}
}

// TestCreateRepoStampingMatrix walks the §2.7.1 decision matrix end-to-end.
// Each case drives the handler with a specific (formidable_first, source_url,
// scaffold_formidable) combination and asserts whether the resulting repo
// carries the .formidable/context.json marker at HEAD — that is the ground
// truth, not the prose in the response message.
func TestCreateRepoStampingMatrix(t *testing.T) {
	source := seedCloneSource(t)

	tru := true
	fal := false

	// wantCommits is the expected number of commits reachable from HEAD
	// after the handler returns. 0 means the repo should be empty
	// (wantNonEmpty must then be false). Clone-only rows count as 1
	// because seedCloneSource's upstream has exactly one commit; stamp-on-
	// clone rows count as 2 (upstream + stamp).
	cases := []struct {
		name          string
		serverDefault bool
		sourceURL     string
		scaffoldFlag  *bool
		wantMarker    bool
		wantNonEmpty  bool
		wantCommits   int
	}{
		{"generic/init/omitted", false, "", nil, false, false, 0},
		{"generic/init/true", false, "", &tru, true, true, 1},
		{"generic/init/false", false, "", &fal, false, false, 0},
		{"generic/clone/omitted", false, source, nil, false, true, 1},
		{"generic/clone/true", false, source, &tru, true, true, 2},
		{"generic/clone/false", false, source, &fal, false, true, 1},
		{"formidable/init/omitted", true, "", nil, true, true, 1},
		{"formidable/init/true", true, "", &tru, true, true, 1},
		{"formidable/init/false", true, "", &fal, false, false, 0},
		{"formidable/clone/omitted", true, source, nil, true, true, 2},
		{"formidable/clone/true", true, source, &tru, true, true, 2},
		{"formidable/clone/false", true, source, &fal, false, true, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := testServer(t)
			srv.cfg.Server.FormidableFirst = c.serverDefault
			repoName := "r-" + strings.ReplaceAll(c.name, "/", "-")

			body := map[string]any{"name": repoName}
			if c.sourceURL != "" {
				body["source_url"] = c.sourceURL
			}
			if c.scaffoldFlag != nil {
				body["scaffold_formidable"] = *c.scaffoldFlag
			}
			payload, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
			if !srv.git.Exists(repoName) {
				t.Fatal("repo should exist after create")
			}

			if c.wantNonEmpty {
				if _, err := srv.git.Head(repoName); err != nil {
					t.Fatalf("HEAD should resolve: %v", err)
				}
			}

			_, err := srv.git.File(repoName, "", formidableMarkerPath)
			hasMarker := err == nil
			if hasMarker != c.wantMarker {
				t.Errorf("marker presence: want %v, got %v (file err: %v)", c.wantMarker, hasMarker, err)
			}

			// Commit count — catches "stamp wrote two commits" or
			// "no-stamp path snuck in a commit anyway" regressions.
			gotCommits := countCommits(t, srv.git.RepoPath(repoName))
			if gotCommits != c.wantCommits {
				t.Errorf("commit count: want %d, got %d", c.wantCommits, gotCommits)
			}
		})
	}
}

// TestCreateRepoClonePreservesMarkerFillsShape confirms the clone-and-
// ensure behaviour on a source that carries a valid marker but is
// missing the templates/ and storage/ starter layout (the real-world
// BrainDamage case). The existing marker must survive byte-for-byte
// — `scaffolded_at` stays on the source's timestamp — while the
// missing scaffold pieces get added in one extra commit so the repo
// is actually usable as a Formidable context.
//
// Parametrised over both paths that reach the stamp code: server
// default (formidable_first=true, flag omitted) and explicit per-
// request opt-in (formidable_first=false, flag=true). Both should
// behave identically against a pre-marked upstream.
func TestCreateRepoClonePreservesMarkerFillsShape(t *testing.T) {
	tru := true

	cases := []struct {
		name          string
		serverDefault bool
		scaffoldFlag  *bool
	}{
		{"server-default", true, nil},
		{"explicit-opt-in", false, &tru},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := seedCloneSourceWithMarker(t)

			srv := testServer(t)
			srv.cfg.Server.FormidableFirst = c.serverDefault

			body := map[string]any{"name": "idem", "source_url": source}
			if c.scaffoldFlag != nil {
				body["scaffold_formidable"] = *c.scaffoldFlag
			}
			payload, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
			// One shape-fill commit on top of the source's two.
			if got := countCommits(t, srv.git.RepoPath("idem")); got != 3 {
				t.Errorf("want 3 commits (source's 2 + 1 shape-fill), got %d", got)
			}
			// Marker byte-stable: scaffolded_at stays on the source's
			// timestamp, proving we did not rewrite a valid marker.
			marker, err := srv.git.File("idem", "", formidableMarkerPath)
			if err != nil {
				t.Fatalf("marker missing: %v", err)
			}
			raw, _ := base64.StdEncoding.DecodeString(marker.ContentB64)
			if !strings.Contains(string(raw), `"scaffolded_at":"2024-01-01T00:00:00Z"`) {
				t.Errorf("marker should keep source's scaffolded_at; got:\n%s", raw)
			}
			// templates/ and storage/ starter pieces must now land.
			for _, p := range []string{"templates/basic.yaml", "storage/.gitkeep"} {
				if _, err := srv.git.File("idem", "", p); err != nil {
					t.Errorf("%s should exist after clone+ensure: %v", p, err)
				}
			}
		})
	}
}

// TestCreateRepoCloneIsNoOpWhenSourceAlreadyComplete — the stricter
// idempotence check: a source that already carries marker + templates/
// + storage/ must produce no new commit when cloned. Proves the
// ensureFormidableShape no-op path works end-to-end through the
// clone handler, not just at the helper level.
func TestCreateRepoCloneIsNoOpWhenSourceAlreadyComplete(t *testing.T) {
	source := seedCloneSourceWithFullFormidable(t)
	sourceHEAD, err := exec.Command("git", "-C", source, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse source: %v", err)
	}
	wantHEAD := strings.TrimSpace(string(sourceHEAD))
	wantCommits := countCommits(t, source)

	srv := testServer(t)
	srv.cfg.Server.FormidableFirst = true

	body, _ := json.Marshal(map[string]any{"name": "idem-full", "source_url": source})
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	head, err := srv.git.Head("idem-full")
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	if head.Version != wantHEAD {
		t.Errorf("HEAD should equal source HEAD (no shape-fill commit), got %s vs %s",
			head.Version, wantHEAD)
	}
	if got := countCommits(t, srv.git.RepoPath("idem-full")); got != wantCommits {
		t.Errorf("commit count should match source (%d), got %d", wantCommits, got)
	}
}

// seedCloneSourceWithMarker is seedCloneSource plus a .formidable/context.json
// committed alongside the README — used to prove stamp idempotence.
func seedCloneSourceWithMarker(t *testing.T) string {
	t.Helper()
	dir := seedCloneSource(t)
	markerDir := filepath.Join(dir, ".formidable")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := []byte(`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}` + "\n")
	if err := os.WriteFile(filepath.Join(markerDir, "context.json"), body, 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	cmds := [][]string{
		{"-C", dir, "add", ".formidable/context.json"},
		{"-C", dir, "commit", "-m", "add marker"},
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// seedCloneSourceWithFullFormidable is seedCloneSourceWithMarker plus
// the templates/basic.yaml and storage/.gitkeep starter files — the
// full Formidable layout already present in the upstream. Used by the
// clone-noop idempotence test: when a source already has everything,
// ensureFormidableShape must add nothing.
func seedCloneSourceWithFullFormidable(t *testing.T) string {
	t.Helper()
	dir := seedCloneSourceWithMarker(t)
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0755); err != nil {
		t.Fatalf("mkdir storage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "basic.yaml"), []byte("name: Basic\n"), 0644); err != nil {
		t.Fatalf("write templates/basic.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "storage", ".gitkeep"), nil, 0644); err != nil {
		t.Fatalf("write storage/.gitkeep: %v", err)
	}
	cmds := [][]string{
		{"-C", dir, "add", "templates", "storage"},
		{"-C", dir, "commit", "-m", "add formidable layout"},
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// seedCloneSourceWithBrokenMarker is seedCloneSource plus a malformed
// .formidable/context.json (non-JSON) commit. Used to prove the stamp
// path replaces a garbage marker with a valid one at the handler layer.
func seedCloneSourceWithBrokenMarker(t *testing.T) string {
	t.Helper()
	dir := seedCloneSource(t)
	markerDir := filepath.Join(dir, ".formidable")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "context.json"), []byte("this is not json\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	cmds := [][]string{
		{"-C", dir, "add", ".formidable/context.json"},
		{"-C", dir, "commit", "-m", "add broken marker"},
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// TestCreateRepoCloneOverwritesBrokenMarker closes the unit↔handler gap:
// the stamp helper knows how to overwrite a broken marker
// (TestStampFormidableMarker_OverwritesBrokenMarker), but that behaviour
// only matters if the create-repo handler actually routes a broken-marker
// clone through the stamper. This covers both server-mode paths so a
// future refactor can't accidentally make one of them skip the overwrite.
func TestCreateRepoCloneOverwritesBrokenMarker(t *testing.T) {
	tru := true
	cases := []struct {
		name          string
		serverDefault bool
		scaffoldFlag  *bool
	}{
		{"formidable-first server default", true, nil},
		{"generic server with explicit opt-in", false, &tru},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := seedCloneSourceWithBrokenMarker(t)
			srv := testServer(t)
			srv.cfg.Server.FormidableFirst = c.serverDefault

			body := map[string]any{"name": "fix-me", "source_url": source}
			if c.scaffoldFlag != nil {
				body["scaffold_formidable"] = *c.scaffoldFlag
			}
			payload, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status: want 201, got %d: %s", rec.Code, rec.Body.String())
			}

			// Marker at HEAD must be valid after the stamp overwrite —
			// not the "this is not json" blob from the source.
			file, err := srv.git.File("fix-me", "", formidableMarkerPath)
			if err != nil {
				t.Fatalf("marker file at HEAD: %v", err)
			}
			raw, decodeErr := base64.StdEncoding.DecodeString(file.ContentB64)
			if decodeErr != nil {
				t.Fatalf("marker b64 decode: %v", decodeErr)
			}
			if !isValidFormidableMarker(raw) {
				t.Errorf("marker should be valid after overwrite; got:\n%s", raw)
			}

			// Source had 2 commits (readme + broken marker). Stamp adds
			// exactly 1 overwrite commit = 3.
			if got := countCommits(t, srv.git.RepoPath("fix-me")); got != 3 {
				t.Errorf("commit count: want 3 (source 2 + stamp 1), got %d", got)
			}
		})
	}
}

func TestCreateRepoCloneInvalidSource(t *testing.T) {
	srv := testServer(t)
	payload := `{"name":"broken","source_url":"/definitely/not/a/git/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if srv.git.Exists("broken") {
		t.Error("repo must not exist after failed clone")
	}
}

func TestReposMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/repos", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestRepoMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/repos/something", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
