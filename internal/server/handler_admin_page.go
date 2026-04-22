package server

import (
	"html/template"
	"net/http"
)

// handleAdminPage serves the /admin login card. No session check here —
// if a session already exists, login.js in the browser bounces the
// caller to /admin/repositories on boot.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminPageTmpl.Execute(w, nil)
}

// adminPageHandler returns an http.HandlerFunc that serves the given
// template at exactly the given path(s). Used by the three peer admin
// pages — each is a thin static shell; all behaviour lives in its JS
// bundle, which guards the session on boot and bounces to /admin on a
// 401. Kept as a small factory so adding a fourth page is one line.
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
		_ = tmpl.Execute(w, nil)
	}
}

// handleCredentialsPage kept as a standalone function so the call site
// in server.go stays readable; it just delegates to the shared factory.
func (s *Server) handleCredentialsPage(w http.ResponseWriter, r *http.Request) {
	s.adminPageHandler(credentialsPageTmpl, "/admin/credentials", "/admin/credentials/")(w, r)
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
	_ = userPageTmpl.Execute(w, nil)
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
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>GiGot — Registration disabled</title>
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
  <img class="logo" src="/assets/gigot.png" alt="GiGot">
  <div class="brand-name">GiGot</div>
  <div class="brand-tag">Registration disabled</div>
  <p class="muted">This server doesn't accept self-service registration. Ask an administrator to create an account for you, or sign in with a configured identity provider.</p>
  <p class="login-footer"><a href="/admin">Back to sign-in</a></p>
</div>
</body>
</html>`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = registerTmpl.Execute(w, nil)
}
