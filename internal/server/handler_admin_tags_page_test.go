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
	// Page shell asserts: the existing-tags table body and the
	// page assets. The Add-tag drawer markup is now created at
	// runtime by GG.drawer.declareAll inside tags.js (was inline
	// in the template before the DRY refactor) — see
	// templates/fragments/create-tag.html for the form itself.
	// The trigger button + sweep-unused button still live in the
	// template, so we pin those.
	for _, want := range []string{
		`data-drawer-open="create-tag"`,
		`id="tag-rows"`,
		`id="btn-sweep-unused"`,
		`/assets/tags.js`,
		`/assets/tags.css`,
		`/assets/drawer.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
