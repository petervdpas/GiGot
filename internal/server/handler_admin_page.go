package server

import "net/http"

// handleAdminPage serves the main admin SPA (repos + subscription keys).
// Template, CSS, and JS live under templates/ and assets/ and are
// embedded at build time by templates.go and assets.go.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminPageTmpl.Execute(w, nil)
}

// handleCredentialsPage serves the standalone credentials admin page.
// Separate from handleAdminPage so the credentials UI lives at its own
// URL (/admin/credentials) instead of being a panel inside the SPA.
func (s *Server) handleCredentialsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/credentials" && r.URL.Path != "/admin/credentials/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = credentialsPageTmpl.Execute(w, nil)
}
