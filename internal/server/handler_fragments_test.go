package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFragments_HappyPath confirms the basic contract: an admin
// session can fetch a known fragment, the body is the raw HTML
// (no Go-template processing), the ETag header is present, and
// Content-Type is HTML. Pins the wire shape that GG.lazy will
// rely on.
func TestFragments_HappyPath(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/fragments/abilities", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if got := rec.Header().Get("ETag"); got == "" || !strings.HasPrefix(got, `"`) {
		t.Errorf("ETag missing or unquoted: %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "{{#each abilities}}") {
		t.Errorf("fragment body looks server-processed (lost {{ }}): %s", body)
	}
}

// TestFragments_RequiresAdminSession pins the auth fence: without
// the session cookie, fragment fetching is a 401. Fragments encode
// admin-UI shape (which inputs exist, which endpoints get hit), so
// gating prevents recon for free.
func TestFragments_RequiresAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/fragments/abilities", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without session, got %d", rec.Code)
	}
}

// TestFragments_UnknownNameIs404 confirms a typo'd or removed
// fragment name surfaces as a 404 with a helpful error body, not a
// silent 200 with an empty page.
func TestFragments_UnknownNameIs404(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/fragments/no-such-fragment", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fragment not found") {
		t.Errorf("expected 'fragment not found' in body: %s", rec.Body.String())
	}
}

// TestFragments_PathTraversalIs400 pins the input validation: a
// name with a slash is rejected at the boundary, so a malicious
// `/fragments/../templates/admin.html` can't escape the folder.
func TestFragments_PathTraversalIs400(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/fragments/sub/path", nil, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for embedded slash, got %d", rec.Code)
	}
}

// TestFragments_ETagRoundTrip pins the cache contract: a second
// request that sends back the previous response's ETag in
// `If-None-Match` returns 304 with no body, so the browser keeps
// reading from its disk cache. Without this the helper would
// re-download every fragment on every page load.
func TestFragments_ETagRoundTrip(t *testing.T) {
	srv, sess := adminTestServer(t)
	first := do(t, srv, http.MethodGet, "/fragments/abilities", nil, sess)
	if first.Code != http.StatusOK {
		t.Fatalf("first fetch: want 200, got %d", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}

	// Second fetch with If-None-Match — same admin session, same
	// fragment, should 304.
	req := httptest.NewRequest(http.MethodGet, "/fragments/abilities", nil)
	req.Header.Set("If-None-Match", etag)
	req.AddCookie(sess)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("want 304 on revalidate, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 must have empty body, got %d bytes", rec.Body.Len())
	}
}

// TestFragments_MethodNotAllowed pins that POST/PATCH/DELETE on the
// fragments endpoint return 405. The endpoint is read-only.
func TestFragments_MethodNotAllowed(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/fragments/abilities", nil, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 for POST, got %d", rec.Code)
	}
}
