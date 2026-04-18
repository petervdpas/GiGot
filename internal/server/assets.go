package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var assetsFS embed.FS

// handleAssets serves static files from the embedded assets/ dir at
// /assets/<file>. The binary is self-contained; no filesystem lookup
// relative to the working directory.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.StripPrefix("/assets/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}
