package server

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
)

// handleAdminPage serves the login card. No session check here — if a
// session already exists, login.js in the browser bounces the caller
// to /admin/repositories on boot. Reachable at both /admin (historical
// URL, kept for OAuth callbacks, bookmarks, and the dozens of internal
// references in login.js + tests + README) and /signin (the friendlier
// public-facing URL the landing page links to).
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin", "/admin/", "/signin", "/signin/":
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminPageTmpl.Execute(w, s.pageData())
}

// adminPageHandler returns an http.HandlerFunc that serves the given
// template at exactly the given path(s). Used by every peer admin
// page (repositories, subscriptions, credentials, tags, accounts,
// auth) — each is a thin static shell rendered through admin_base.html
// with per-page `{{define}}` overrides; all behaviour lives in its JS
// bundle, which guards the session on boot and bounces to /admin on a
// 401. Kept as a small factory so adding a new page is one line here
// plus one line in templates.go. Every page receives s.pageData()
// so the brand strip + JS sidebar can render "GiGot vX.Y.Z" off one
// source.
func (s *Server) adminPageHandler(tmpl *template.Template, paths ...string) http.HandlerFunc {
	allowed := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		allowed[p] = struct{}{}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.Execute(w, s.pageData())
	}
}

// handleCredentialsPage kept as a standalone function so the call site
// in server.go stays readable; it just delegates to the shared factory.
func (s *Server) handleCredentialsPage(w http.ResponseWriter, r *http.Request) {
	s.adminPageHandler(credentialsPageTmpl, "/admin/credentials", "/admin/credentials/")(w, r)
}

// handleTagsPage serves the /admin/tags catalogue page. Same
// static-shell-plus-JS pattern as the other admin pages.
func (s *Server) handleTagsPage(w http.ResponseWriter, r *http.Request) {
	s.adminPageHandler(tagsPageTmpl, "/admin/tags", "/admin/tags/")(w, r)
}

// handleAccountsPage serves the admin accounts console. Same shape as
// the other admin pages — static HTML shell, session-guarded in JS.
func (s *Server) handleAccountsPage(w http.ResponseWriter, r *http.Request) {
	s.adminPageHandler(accountsPageTmpl, "/admin/accounts", "/admin/accounts/")(w, r)
}

// handleAuthPage serves the /admin/auth console, the UI for the
// hot-reload endpoints. Same static-shell-plus-JS pattern as every
// other admin section.
func (s *Server) handleAuthPage(w http.ResponseWriter, r *http.Request) {
	s.adminPageHandler(authPageTmpl, "/admin/auth", "/admin/auth/")(w, r)
}

// handleUserPage serves the /user self-serve account page. Public
// path (no server-side session gate) — user.js calls /api/me and
// bounces to /admin on a 401. Same pattern as the admin pages.
func (s *Server) handleUserPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/user" && r.URL.Path != "/user/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = userPageTmpl.Execute(w, s.pageData())
}

// handleRegisterPage serves the self-service register card. When
// auth.allow_local is false, the backing /api/register endpoint
// 404s, so the page can't do anything useful — we render a small
// "registration disabled" card instead of a bare 404 so someone
// who bookmarked /admin/register gets a human-readable dead-end.
func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/register" {
		http.NotFound(w, r)
		return
	}
	if !s.allowLocal() {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		// Inline so the disabled-state card stays a single self-
		// contained payload — no template to parse, no JS to load —
		// even if the rest of the assets are unreachable. The brand
		// suffix flows through the same brandVersion() rule as the
		// regular templates.
		v := html.EscapeString(s.brandVersion())
		suffix := ""
		if v != "" {
			suffix = " <span class=\"brand-version muted\">" + v + "</span>"
		}
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GiGot - Registration disabled</title>
<link rel="stylesheet" href="/assets/admin.css">
<script>
  try {
    var t = localStorage.getItem('gigot.theme');
    if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t);
  } catch (e) { /* default dark */ }
</script>
</head>
<body>
<div class="login-wrap card">
  <a href="/" class="logo-link" aria-label="GiGot home"><img class="logo" src="/assets/gigot.png" alt="GiGot"></a>
  <div class="brand-name">GiGot%s</div>
  <div class="brand-tag">Registration disabled</div>
  <p class="muted">This server doesn't accept self-service registration. Ask an administrator to create an account for you, or sign in with a configured identity provider.</p>
  <p class="login-footer"><a href="/admin">Back to sign-in</a></p>
</div>
</body>
</html>`, suffix)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = registerTmpl.Execute(w, s.pageData())
}
