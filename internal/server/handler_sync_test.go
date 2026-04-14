package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRepoLogWithCommits(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("log-commits")

	// Add a commit via a temp clone.
	repoPath := srv.git.RepoPath("log-commits")
	tmpWork := t.TempDir()
	run(t, "git", "clone", repoPath, tmpWork+"/work")
	run(t, "git", "-C", tmpWork+"/work", "commit", "--allow-empty", "-m", "test commit")
	run(t, "git", "-C", tmpWork+"/work", "push", "origin", "master")

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
