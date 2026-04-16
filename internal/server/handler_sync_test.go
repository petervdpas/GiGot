package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
