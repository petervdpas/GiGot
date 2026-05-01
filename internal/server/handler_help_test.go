package server

import (
	"net/http"
	"strings"
	"testing"
)

// /help and /help/<slug> render embedded markdown. The endpoints are
// public — operators must reach them without a session — and slugs
// are validated against the embedded directory listing so a
// path-traversal attempt simply 404s.

func TestHelp_IndexRenders(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/help", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// First H1 from index.md must show up rendered.
	if !strings.Contains(body, "<h1") || !strings.Contains(body, "Help") {
		t.Errorf("expected rendered H1 'Help' in body, got: %s", body)
	}
	// Markdown link to /help/overview must be rewritten to an <a>.
	if !strings.Contains(body, `href="/help/overview"`) {
		t.Errorf("expected anchor href to /help/overview, got: %s", body)
	}
}

func TestHelp_SlugRenders(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/help/overview", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<h1") {
		t.Errorf("expected H1 in rendered overview, got: %s", rec.Body.String())
	}
}

func TestHelp_UnknownSlug404s(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/help/does-not-exist", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

// Path traversal attempts must not escape the embed FS. The slug parser
// rejects anything containing a slash, so the request 404s without ever
// reading from disk.
func TestHelp_PathTraversal404s(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/help/..%2Fserver", nil, nil)
	if rec.Code == http.StatusOK {
		t.Errorf("path traversal should not return 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// /help is public — auth.enabled=true must not gate it. We hit the
// route on a server that has auth on (testServer enables it) without
// any session cookie and expect a normal 200.
func TestHelp_PublicWithoutSession(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/help", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/help must be public, got %d", rec.Code)
	}
}
