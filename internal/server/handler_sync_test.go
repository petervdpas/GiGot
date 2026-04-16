package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

func TestRepoStatusEmpty(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("status-test")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/status-test/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoStatusResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Name != "status-test" {
		t.Errorf("expected name status-test, got %s", body.Name)
	}
	if !body.Empty {
		t.Error("expected empty repo")
	}
}

func TestRepoStatusNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoBranchesEmpty(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("branch-test")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/branch-test/branches", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRepoBranchesNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/branches", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoLogEmpty(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("log-test")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/log-test/log", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoLogResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Count != 0 {
		t.Errorf("expected 0 entries, got %d", body.Count)
	}
}

func TestRepoLogNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/log", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoHeadMissing(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/head", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoHeadEmptyRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("empty-head")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/empty-head/head", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 on empty repo, got %d", rec.Code)
	}
}

func TestRepoHeadPopulated(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("head-ok")
	seedCommit(t, srv, "head-ok", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/head-ok/head", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body gitmanager.HeadInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Version) != 40 {
		t.Errorf("version should be a 40-char SHA, got %q", body.Version)
	}
	if body.DefaultBranch != "master" {
		t.Errorf("default_branch: want master, got %q", body.DefaultBranch)
	}
}

func TestRepoTreeMissing(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/tree", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoTreeEmptyRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("empty-tree")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/empty-tree/tree", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 on empty repo, got %d", rec.Code)
	}
}

func TestRepoTreeBadVersion(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("tree-bad")
	seedCommit(t, srv, "tree-bad", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/tree-bad/tree?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 on bad version, got %d", rec.Code)
	}
}

func TestRepoTreePopulated(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("tree-ok")
	seedFile(t, srv, "tree-ok", "README.md", "hello\n", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/tree-ok/tree", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body gitmanager.TreeInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Version) != 40 {
		t.Errorf("version should be a 40-char SHA, got %q", body.Version)
	}
	if len(body.Files) != 1 {
		t.Fatalf("want 1 file, got %d: %+v", len(body.Files), body.Files)
	}
	if body.Files[0].Path != "README.md" {
		t.Errorf("path: want README.md, got %q", body.Files[0].Path)
	}
}

func TestRepoSnapshotMissing(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/snapshot", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoSnapshotEmptyRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("empty-snap")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/empty-snap/snapshot", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 on empty repo, got %d", rec.Code)
	}
}

func TestRepoSnapshotBadVersion(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("snap-bad")
	seedCommit(t, srv, "snap-bad", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/snap-bad/snapshot?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 on bad version, got %d", rec.Code)
	}
}

func TestRepoSnapshotPopulated(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("snap-ok")
	seedFile(t, srv, "snap-ok", "README.md", "hello\n", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/snap-ok/snapshot", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body gitmanager.SnapshotInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(body.Files))
	}
	content, err := base64.StdEncoding.DecodeString(body.Files[0].ContentB64)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if string(content) != "hello\n" {
		t.Errorf("content: want %q, got %q", "hello\n", content)
	}
}

func TestRepoFileMissing(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/ghost/files/README.md", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoFileEmptyRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("empty-file")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/empty-file/files/README.md", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 on empty repo, got %d", rec.Code)
	}
}

func TestRepoFileBadVersion(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("file-badver")
	seedFile(t, srv, "file-badver", "README.md", "hello\n", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/file-badver/files/README.md?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 on bad version, got %d", rec.Code)
	}
}

func TestRepoFilePathNotFound(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("file-badpath")
	seedFile(t, srv, "file-badpath", "README.md", "hello\n", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/file-badpath/files/does/not/exist.txt", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 on missing path, got %d", rec.Code)
	}
}

func TestRepoFilePopulated(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("file-ok")
	seedFile(t, srv, "file-ok", "docs/notes.md", "body\n", "seed")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/file-ok/files/docs/notes.md", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body gitmanager.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Path != "docs/notes.md" {
		t.Errorf("path: want docs/notes.md, got %q", body.Path)
	}
	content, err := base64.StdEncoding.DecodeString(body.ContentB64)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if string(content) != "body\n" {
		t.Errorf("content: want %q, got %q", "body\n", content)
	}
}

// seedCommit creates an empty commit on the given repo's master branch.
// Useful when the test only needs HEAD to exist.
func seedCommit(t *testing.T, srv *Server, repo, message string) {
	t.Helper()
	repoPath := srv.git.RepoPath(repo)
	work := t.TempDir() + "/work"
	run(t, "git", "clone", repoPath, work)
	run(t, "git", "-C", work, "commit", "--allow-empty", "-m", message)
	run(t, "git", "-C", work, "push", "origin", "master")
}

// seedFile writes and pushes one file in a fresh commit on master. Nested
// parent directories are created as needed so paths like "docs/notes.md"
// work without the caller pre-seeding the tree.
func seedFile(t *testing.T, srv *Server, repo, path, content, message string) {
	t.Helper()
	repoPath := srv.git.RepoPath(repo)
	work := t.TempDir() + "/work"
	run(t, "git", "clone", repoPath, work)
	full := work + "/" + path
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	run(t, "git", "-C", work, "add", path)
	run(t, "git", "-C", work, "commit", "-m", message)
	run(t, "git", "-C", work, "push", "origin", "master")
}

// putFile builds and dispatches a PUT /files/{path} request and returns the
// recorder. Shared across the write-path tests.
func putFile(t *testing.T, srv *Server, repo, path, parent, content string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{
		"parent_version": parent,
		"content_b64":    base64.StdEncoding.EncodeToString([]byte(content)),
		"message":        "test",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/repos/"+repo+"/files/"+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// seedBareWith creates a bare repo plus a single seed file so the handler
// tests have a well-known HEAD to write against.
func seedBareWith(t *testing.T, srv *Server, repo, path, content string) string {
	t.Helper()
	if err := srv.git.InitBare(repo); err != nil {
		t.Fatalf("init %s: %v", repo, err)
	}
	seedFile(t, srv, repo, path, content, "seed")
	head, err := srv.git.Head(repo)
	if err != nil {
		t.Fatalf("head %s: %v", repo, err)
	}
	return head.Version
}

func TestRepoFilePutFastForward(t *testing.T) {
	srv := testServer(t)
	parent := seedBareWith(t, srv, "ff-put", "a.txt", "one\n")

	rec := putFile(t, srv, "ff-put", "a.txt", parent, "two\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var res gitmanager.WriteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Version == parent || len(res.Version) != 40 {
		t.Errorf("Version should advance to new 40-char SHA, got %q", res.Version)
	}
	if res.MergedFrom != "" || res.MergedWith != "" {
		t.Errorf("fast-forward should not set merge fields: %+v", res)
	}
}

func TestRepoFilePutAutoMerge(t *testing.T) {
	srv := testServer(t)
	parent := seedBareWith(t, srv, "am-put", "a.txt", "A\n")
	// Server advances on a different file → client's parent is now stale
	// but a clean merge is possible.
	seedFile(t, srv, "am-put", "b.txt", "B\n", "server adds b")

	rec := putFile(t, srv, "am-put", "a.txt", parent, "A edited\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var res gitmanager.WriteResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.MergedFrom != parent {
		t.Errorf("MergedFrom: want %s, got %s", parent, res.MergedFrom)
	}
	if res.MergedWith == "" {
		t.Error("MergedWith should be populated on auto-merge")
	}
}

func TestRepoFilePutConflict(t *testing.T) {
	srv := testServer(t)
	parent := seedBareWith(t, srv, "cf-put", "a.txt", "original\n")
	seedFile(t, srv, "cf-put", "a.txt", "server-change\n", "server edit")

	rec := putFile(t, srv, "cf-put", "a.txt", parent, "client-change\n")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body WriteFileConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Path != "a.txt" {
		t.Errorf("Path: want a.txt, got %q", body.Path)
	}
	if body.BaseB64 == "" || body.TheirsB64 == "" || body.YoursB64 == "" {
		t.Errorf("all three blob fields should be populated; got %+v", body)
	}
}

func TestRepoFilePutStaleParent(t *testing.T) {
	srv := testServer(t)
	seedBareWith(t, srv, "sp-put", "a.txt", "v1\n")

	// Plant an unrelated commit so a non-ancestor parent_version still
	// resolves. Same trick as TestWriteFileStaleParent in the manager tests.
	otherDir := t.TempDir() + "/other"
	run(t, "git", "init", otherDir)
	run(t, "git", "-C", otherDir, "config", "user.email", "x@y.z")
	run(t, "git", "-C", otherDir, "config", "user.name", "x")
	os.WriteFile(otherDir+"/x.txt", []byte("x\n"), 0644)
	run(t, "git", "-C", otherDir, "add", "x.txt")
	run(t, "git", "-C", otherDir, "commit", "-m", "orphan")
	shaOut, err := exec.Command("git", "-C", otherDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse orphan: %v", err)
	}
	orphanSHA := strings.TrimSpace(string(shaOut))
	out, err := exec.Command("git", "-C", srv.git.RepoPath("sp-put"), "fetch", otherDir, orphanSHA).CombinedOutput()
	if err != nil {
		t.Fatalf("fetch orphan: %s: %v", out, err)
	}

	rec := putFile(t, srv, "sp-put", "a.txt", orphanSHA, "client\n")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 stale-parent, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body WriteFileConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.BaseB64 != "" || body.TheirsB64 != "" {
		t.Errorf("stale-parent 409 must not carry base/theirs; got %+v", body)
	}
	if body.YoursB64 == "" {
		t.Error("stale-parent 409 should echo yours_b64")
	}
	if body.CurrentVersion == "" {
		t.Error("stale-parent 409 should include current_version")
	}
}

func TestRepoFilePutBadParent(t *testing.T) {
	srv := testServer(t)
	seedBareWith(t, srv, "bad-put", "a.txt", "x\n")

	rec := putFile(t, srv, "bad-put", "a.txt",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "y\n")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 on bad parent, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRepoFilePutMissingRepo(t *testing.T) {
	srv := testServer(t)
	rec := putFile(t, srv, "ghost", "a.txt", "HEAD", "x\n")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRepoFilePutEmptyRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("empty-put")
	rec := putFile(t, srv, "empty-put", "a.txt", "HEAD", "x\n")
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 on empty repo, got %d", rec.Code)
	}
}

func TestRepoFilePutMissingParent(t *testing.T) {
	srv := testServer(t)
	seedBareWith(t, srv, "noparent", "a.txt", "x\n")

	req := httptest.NewRequest(http.MethodPut, "/api/repos/noparent/files/a.txt",
		strings.NewReader(`{"content_b64":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRepoFilePutInvalidPath(t *testing.T) {
	srv := testServer(t)
	parent := seedBareWith(t, srv, "ip-put", "a.txt", "A\n")

	// Go's http.ServeMux cleans `..` out of the request path before we see
	// it, so use a different form git also rejects — anything under .git/.
	rec := putFile(t, srv, "ip-put", ".git/config", parent, "x\n")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on invalid path, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRepoFilePutBadBase64(t *testing.T) {
	srv := testServer(t)
	parent := seedBareWith(t, srv, "badb64", "a.txt", "x\n")
	body := `{"parent_version":"` + parent + `","content_b64":"not base64!!"}`
	req := httptest.NewRequest(http.MethodPut, "/api/repos/badb64/files/a.txt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on bad base64, got %d", rec.Code)
	}
}

func TestRepoLogWithCommits(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("log-commits")
	seedCommit(t, srv, "log-commits", "test commit")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/log-commits/log", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoLogResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Count != 1 {
		t.Errorf("expected 1 entry, got %d", body.Count)
	}
	if body.Count > 0 && body.Entries[0].Message != "test commit" {
		t.Errorf("expected message 'test commit', got %s", body.Entries[0].Message)
	}
}
