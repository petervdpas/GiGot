package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTagsPage_RendersShell(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/tags", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="tag-form"`, `id="tag-rows"`, `/assets/tags.js`, `/assets/tags.css`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
