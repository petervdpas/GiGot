package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.RepoRoot = dir
	cfg.Crypto.PrivateKeyPath = filepath.Join(dir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(dir, "server.pub")
	cfg.Crypto.DataDir = filepath.Join(dir, "data")
	return New(cfg)
}

func TestIndexPage(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "GiGot") {
		t.Error("index page should contain GiGot")
	}
	if !strings.Contains(body, "Running") {
		t.Error("index page should show running status")
	}
}

func TestIndexPageShowsRepoCount(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("repo-a")
	srv.git.InitBare("repo-b")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "2") {
		t.Error("index page should show repo count of 2")
	}
}

func TestIndexPageLinksToSwagger(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "/swagger/") {
		t.Error("index page should link to swagger docs")
	}
}

func TestNotFoundForUnknownPaths(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
