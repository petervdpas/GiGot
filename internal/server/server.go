package server

import (
	"fmt"
	"html/template"
	"net/http"
	"runtime"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/config"
	gitmanager "github.com/petervdpas/GiGot/internal/git"

	httpSwagger "github.com/swaggo/http-swagger"

	// Import generated docs.
	_ "github.com/petervdpas/GiGot/docs"
)

// Server is the GiGot HTTP server.
type Server struct {
	cfg           *config.Config
	git           *gitmanager.Manager
	auth          *auth.Provider
	tokenStrategy *auth.TokenStrategy
	mux           *http.ServeMux
}

// New creates a new Server instance.
func New(cfg *config.Config) *Server {
	ap := auth.NewProvider()
	ap.SetEnabled(cfg.Auth.Enabled)

	ts := auth.NewTokenStrategy()
	ap.Register(ts)

	s := &Server{
		cfg:           cfg,
		git:           gitmanager.NewManager(cfg.Storage.RepoRoot),
		auth:          ap,
		tokenStrategy: ts,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s
}

// Auth returns the auth provider for registration of strategies.
func (s *Server) Auth() *auth.Provider {
	return s.auth
}

// TokenStrategy returns the token strategy for external token management.
func (s *Server) TokenStrategy() *auth.TokenStrategy {
	return s.tokenStrategy
}

// Handler returns the HTTP handler (with auth middleware) for use in tests.
func (s *Server) Handler() http.Handler {
	return s.auth.Middleware(s.mux)
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	return http.ListenAndServe(addr, s.Handler())
}

// routes registers all HTTP handlers.
func (s *Server) routes() {
	// Pages
	s.mux.HandleFunc("/", s.handleIndex)

	// Swagger
	s.mux.Handle("/swagger/", httpSwagger.WrapHandler)

	// API
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/repos", s.handleRepos)
	s.mux.HandleFunc("/api/repos/", s.handleRepoRouter)
	s.mux.HandleFunc("/api/auth/token", s.handleToken)

	// Git smart HTTP transport
	s.mux.HandleFunc("/git/", s.handleGitRouter)
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
    <div><a href="/swagger/index.html">API Documentation</a></div>
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
