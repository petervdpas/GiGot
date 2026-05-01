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

// The landing page surfaces a single "Help" link as its primary CTA;
// /help in turn links out to Swagger. This pair of asserts locks the
// two-hop discoverability path so a future "drop the help link" or
// "drop the swagger link in help" change fails fast.
func TestIndexLinksToHelpAndHelpLinksToSwagger(t *testing.T) {
	srv := testServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `href="/help"`) {
		t.Error("index page should link to /help")
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/help", nil))
	if !strings.Contains(rec.Body.String(), "/swagger/") {
		t.Error("/help should link to swagger docs")
	}
}

// /signin is a public alias for /admin so the landing page can offer a
// "Sign in" link that doesn't read like an admin-only URL. Both paths
// must serve the same login card; tests pin both ends so a "clean up
// duplicate routes" pass can't silently drop the alias.
func TestSigninAliasServesLoginCard(t *testing.T) {
	srv := testServer(t)

	for _, path := range []string{"/admin", "/signin"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "login-form") {
			t.Errorf("%s: expected login form in body", path)
		}
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
