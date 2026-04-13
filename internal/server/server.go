package server

import (
	"fmt"
	"html/template"
	"net/http"
	"runtime"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/config"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// Server is the GiGot HTTP server.
type Server struct {
	cfg     *config.Config
	git     *gitmanager.Manager
	auth    *auth.Provider
	mux     *http.ServeMux
}

// New creates a new Server instance.
func New(cfg *config.Config) *Server {
	s := &Server{
		cfg:  cfg,
		git:  gitmanager.NewManager(cfg.Storage.RepoRoot),
		auth: auth.NewProvider(),
		mux:  http.NewServeMux(),
	}
	s.routes()
	return s
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	return http.ListenAndServe(addr, s.mux)
}

// routes registers all HTTP handlers.
func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/repos", s.handleRepos)
	s.mux.HandleFunc("/api/repos/", s.handleRepo)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	// TODO: list repos, create repo
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	// TODO: repo details, git smart HTTP transport
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

var indexTmpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>GiGot</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
         background: #0d1117; color: #c9d1d9; display: flex; align-items: center;
         justify-content: center; min-height: 100vh; }
  .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px;
          padding: 2.5rem; max-width: 420px; text-align: center; }
  h1 { font-size: 2rem; margin-bottom: 0.25rem; color: #f0f6fc; }
  .subtitle { color: #8b949e; margin-bottom: 1.5rem; }
  .status { display: inline-block; background: #238636; color: #fff;
            padding: 0.25rem 0.75rem; border-radius: 12px; font-size: 0.85rem;
            margin-bottom: 1.5rem; }
  .info { text-align: left; font-size: 0.9rem; line-height: 1.8; }
  .info span { color: #8b949e; }
  a { color: #58a6ff; text-decoration: none; }
</style>
</head>
<body>
<div class="card">
  <h1>GiGot</h1>
  <p class="subtitle">Git-backed server for Formidable</p>
  <div class="status">running</div>
  <div class="info">
    <div><span>Port:</span> {{.Port}}</div>
    <div><span>Repo root:</span> {{.RepoRoot}}</div>
    <div><span>Repos:</span> {{.RepoCount}}</div>
    <div><span>Go:</span> {{.GoVersion}}</div>
  </div>
</div>
</body>
</html>`))

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	repos, _ := s.git.List()

	data := struct {
		Port      int
		RepoRoot  string
		RepoCount int
		GoVersion string
	}{
		Port:      s.cfg.Server.Port,
		RepoRoot:  s.cfg.Storage.RepoRoot,
		RepoCount: len(repos),
		GoVersion: runtime.Version(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	indexTmpl.Execute(w, data)
}
