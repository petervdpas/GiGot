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
	// Page shell asserts: the data-driven drawer markup (Add-tag
	// drawer + tpl reference), the existing-tags table body, and
	// the page assets. The form itself lives in the create-tag
	// fragment, fetched on first drawer open — it's not in the
	// page body, so we don't pin id="tag-form" here.
	for _, want := range []string{
		`data-drawer-name="create-tag"`,
		`data-lazy-tpl="create-tag"`,
		`id="tag-rows"`,
		`/assets/tags.js`,
		`/assets/tags.css`,
		`/assets/drawer.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
