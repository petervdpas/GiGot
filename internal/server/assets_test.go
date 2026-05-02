package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /assets/* serves the embedded minified bundle. These tests pin the
// two contracts that matter for responsiveness: gzipped representation
// is offered to callers that advertise it, and the raw form is the
// fallback for those that don't.

func TestAssets_GzipServedWhenAccepted(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/admin.css", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("expected Vary: Accept-Encoding, got %q", rec.Header().Get("Vary"))
	}
	// Body must round-trip back through gunzip — guards against a
	// future change that sets the header without actually compressing.
	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if _, err := io.ReadAll(gz); err != nil {
		t.Fatalf("read gunzipped body: %v", err)
	}
}

func TestAssets_RawWhenGzipNotAccepted(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/admin.css", nil)
	// Deliberately omit Accept-Encoding.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("expected no Content-Encoding, got %q", got)
	}
	// CSS body must look like CSS.
	if !bytes.Contains(rec.Body.Bytes(), []byte("--bg")) &&
		!bytes.Contains(rec.Body.Bytes(), []byte("body")) {
		t.Errorf("body doesn't look like CSS: %q", rec.Body.String()[:min(120, rec.Body.Len())])
	}
}

func TestAssets_AlreadyCompressedNotRegzipped(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/gigot.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("PNG should not be re-gzipped, got Content-Encoding: %q", got)
	}
}

func TestAssets_UnknownPath404s(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestAssets_PathTraversal404s(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/../assets.go", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("path traversal should not return 200, got %d", rec.Code)
	}
}

