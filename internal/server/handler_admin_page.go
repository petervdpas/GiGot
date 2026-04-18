package server

import "net/http"

// handleAdminPage serves the single-page admin UI. Template, CSS, and
// JS live under templates/ and assets/ and are embedded at build time
// by templates.go and assets.go.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminPageTmpl.Execute(w, nil)
}
