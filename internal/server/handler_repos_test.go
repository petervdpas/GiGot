package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCreateRepoCloneAndScaffoldMutuallyExclusive(t *testing.T) {
	srv := testServer(t)
	payload := `{"name":"nope","source_url":"https://example.com/x.git","scaffold_formidable":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if srv.git.Exists("nope") {
		t.Error("repo must not be created when request is rejected")
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
