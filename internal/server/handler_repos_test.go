package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListReposEmpty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

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

	srv.mux.ServeHTTP(rec, req)

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

	srv.mux.ServeHTTP(rec, req)

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

	srv.mux.ServeHTTP(rec, req)

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

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rec.Code)
	}
}

func TestCreateRepoInvalidBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGetRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("my-repo")

	req := httptest.NewRequest(http.MethodGet, "/api/repos/my-repo", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body RepoInfo
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Name != "my-repo" {
		t.Errorf("expected name my-repo, got %s", body.Name)
	}
}

func TestGetRepoNotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/repos/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("doomed")

	req := httptest.NewRequest(http.MethodDelete, "/api/repos/doomed", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

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

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestReposMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/repos", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestRepoMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/repos/something", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
