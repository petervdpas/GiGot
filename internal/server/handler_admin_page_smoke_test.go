package server

import (
	"net/http"
	"strings"
	"testing"
)

// TestAdminPages_Smoke is the catch-net for "did the template parse +
// execute against pageData() without erroring." Each admin page is a
// thin static shell whose behaviour lives in JS, so the failure mode
// the test catches is a busted template (missing block, wrong
// reference, mis-renamed field) — exactly the kind of regression
// that would otherwise only surface when an admin loaded the page in
// a browser.
//
// The pages are the seven `handle*Page` functions reported as 0%
// coverage by go test -cover before this slice landed; folding them
// into one table is the smallest unit of value (and the smallest
// unit of churn the next time a page is added).
//
// Auth: every admin page is a static HTML shell. The session gate
// lives in the page JS, not the handler, so an unauthenticated GET
// renders the template just fine — the shipped page then bounces
// the browser to /admin via login.js. A test that hit the JS would
// be a different kind of test; this one asserts the server-side
// rendering contract.
func TestAdminPages_Smoke(t *testing.T) {
	srv := testServer(t)

	cases := []struct {
		name string
		path string
		// Expected substring in the rendered body. Each page's title
		// block lands in admin_base.html's <title>, so picking that
		// distinguishes one page's render from another's and proves
		// the per-page {{define "title"}} actually overrode the
		// base block.
		wantTitle string
	}{
		{"settings", "/admin/settings", "Settings"},
		{"credentials", "/admin/credentials", "Credentials"},
		{"accounts", "/admin/accounts", "Accounts"},
		{"tags", "/admin/tags", "Tags"},
		{"auth", "/admin/auth", "Authentication"},
		{"benchmark", "/admin/benchmark", "Benchmark"},
		{"user", "/user", "subscriptions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodGet, tc.path, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s want 200, got %d body=%s", tc.path, rec.Code, rec.Body.String())
			}
			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("%s want text/html content-type, got %q", tc.path, ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "GiGot") {
				t.Errorf("%s body missing brand string (template skeleton broken?)", tc.path)
			}
			if !strings.Contains(body, tc.wantTitle) {
				t.Errorf("%s body missing %q (per-page title block not rendering?)", tc.path, tc.wantTitle)
			}
		})
	}
}

// TestAdminPages_TrailingSlashAllowed — every page route is registered
// at both `/admin/foo` and `/admin/foo/` so a stray trailing slash
// resolves rather than 404ing. Pinning this stops a routing
// refactor from silently dropping the alias.
func TestAdminPages_TrailingSlashAllowed(t *testing.T) {
	srv := testServer(t)
	paths := []string{
		"/admin/settings/",
		"/admin/credentials/",
		"/admin/accounts/",
		"/admin/tags/",
		"/admin/auth/",
		"/admin/benchmark/",
		"/user/",
	}
	for _, p := range paths {
		rec := do(t, srv, http.MethodGet, p, nil, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s want 200, got %d", p, rec.Code)
		}
	}
}

// TestRegisterPage_AllowLocalEnabled — /admin/register is the
// self-service registration card; the handler has a documented
// branch for auth.allow_local=false (renders an inline "registration
// disabled" payload with HTTP 404). Default config has allow_local
// true, so the happy path is what the smoke test exercises.
func TestRegisterPage_AllowLocalEnabled(t *testing.T) {
	srv := testServer(t)
	rec := do(t, srv, http.MethodGet, "/admin/register", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/admin/register want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GiGot") {
		t.Error("register page missing brand string")
	}
}

// TestRegisterPage_AllowLocalDisabled — when local auth is off, the
// handler returns a self-contained 404 with a "registration disabled"
// card so a bookmark to /admin/register doesn't dead-end on a bare
// 404. This is the OTHER branch of handleRegisterPage that the
// smoke test above doesn't hit.
func TestRegisterPage_AllowLocalDisabled(t *testing.T) {
	srv := testServer(t)
	srv.cfg.Auth.AllowLocal = false
	rec := do(t, srv, http.MethodGet, "/admin/register", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with allow_local=false, /admin/register want 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Registration disabled") {
		t.Errorf("disabled-state body missing the explanation card; got: %s", rec.Body.String())
	}
}
