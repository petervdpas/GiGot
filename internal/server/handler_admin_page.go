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
