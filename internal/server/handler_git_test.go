package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitInfoRefsUploadPack(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("test-repo")

	req := httptest.NewRequest(http.MethodGet, "/git/test-repo.git/info/refs?service=git-upload-pack", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-git-upload-pack-advertisement" {
		t.Errorf("expected git content type, got %s", ct)
	}

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("git-upload-pack")) {
		t.Error("response should contain service announcement")
	}
}

func TestGitInfoRefsReceivePack(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("test-repo")

	req := httptest.NewRequest(http.MethodGet, "/git/test-repo.git/info/refs?service=git-receive-pack", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-git-receive-pack-advertisement" {
		t.Errorf("expected git content type, got %s", ct)
	}
}

func TestGitInfoRefsInvalidService(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("test-repo")

	req := httptest.NewRequest(http.MethodGet, "/git/test-repo.git/info/refs?service=invalid", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGitInfoRefsNotFound(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/git/nope.git/info/refs?service=git-upload-pack", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGitCloneIntegration(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("clone-test")

	// Add a commit to the bare repo so there's something to clone.
	repoPath := srv.git.RepoPath("clone-test")
	tmpWork := t.TempDir()
	run(t, "git", "clone", repoPath, tmpWork+"/work")
	run(t, "git", "-C", tmpWork+"/work", "commit", "--allow-empty", "-m", "initial")
	run(t, "git", "-C", tmpWork+"/work", "push", "origin", "master")

	// Start test server.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Clone over HTTP from GiGot.
	cloneDest := filepath.Join(t.TempDir(), "cloned")
	cmd := exec.Command("git", "clone", ts.URL+"/git/clone-test.git", cloneDest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone failed: %s\n%s", err, string(out))
	}

	// Verify the clone has the commit.
	logCmd := exec.Command("git", "-C", cloneDest, "log", "--oneline")
	logOut, err := logCmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !bytes.Contains(logOut, []byte("initial")) {
		t.Errorf("cloned repo should contain 'initial' commit, got: %s", string(logOut))
	}
}

func TestGitPushIntegration(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("push-test")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Clone the empty repo.
	cloneDest := filepath.Join(t.TempDir(), "work")
	run(t, "git", "clone", ts.URL+"/git/push-test.git", cloneDest)

	// Make a commit and push.
	run(t, "git", "-C", cloneDest, "commit", "--allow-empty", "-m", "pushed-commit")
	cmd := exec.Command("git", "-C", cloneDest, "push", "origin", "master")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git push failed: %s\n%s", err, string(out))
	}

	// Verify the push landed in the bare repo.
	repoPath := srv.git.RepoPath("push-test")
	logCmd := exec.Command("git", "-C", repoPath, "log", "--oneline")
	logOut, err := logCmd.Output()
	if err != nil {
		t.Fatalf("git log on bare repo failed: %v", err)
	}
	if !bytes.Contains(logOut, []byte("pushed-commit")) {
		t.Errorf("bare repo should contain 'pushed-commit', got: %s", string(logOut))
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %s\n%s", name, args, err, string(out))
	}
}
