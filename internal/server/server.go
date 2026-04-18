package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"path/filepath"

	"github.com/petervdpas/GiGot/internal/admins"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/clients"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/crypto"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"

	httpSwagger "github.com/swaggo/http-swagger"

	// Import generated docs.
	_ "github.com/petervdpas/GiGot/docs"
)

// Server is the GiGot HTTP server.
type Server struct {
	cfg             *config.Config
	git             *gitmanager.Manager
	auth            *auth.Provider
	tokenStrategy   *auth.TokenStrategy
	sessionStrategy *auth.SessionStrategy
	encryptor       *crypto.Encryptor
	clients         *clients.Store
	admins          *admins.Store
	policy          policy.Evaluator
	mux             *http.ServeMux
}

// New creates a new Server instance. A server keypair is loaded from
// cfg.Crypto (or generated on first run). Panics if the keypair cannot be
// loaded, because the server cannot safely operate without one.
func New(cfg *config.Config) *Server {
	ap := auth.NewProvider()
	ap.SetEnabled(cfg.Auth.Enabled)

	// Endpoints that must work before any bearer token or session exists.
	ap.MarkPublic("/")
	ap.MarkPublic("/api/crypto/pubkey")
	ap.MarkPublic("/api/clients/enroll")
	ap.MarkPublic("/api/admin/session") // returns 401 internally for the page to decide
	ap.MarkPublic("/admin")
	ap.MarkPublic("/admin/")
	ap.MarkPublic("/admin/login")
	ap.MarkPublic("/admin/logout")
	ap.MarkPublicPrefix("/swagger/")
	ap.MarkPublicPrefix("/assets/")

	ts := auth.NewTokenStrategy()
	ap.Register(ts)

	enc, generated, err := crypto.LoadOrGenerate(cfg.Crypto.PrivateKeyPath, cfg.Crypto.PublicKeyPath)
	if err != nil {
		log.Fatalf("server: load/generate keypair: %v", err)
	}
	if generated {
		log.Printf("server: generated new NaCl keypair at %s / %s", cfg.Crypto.PrivateKeyPath, cfg.Crypto.PublicKeyPath)
	}

	clientStore, err := clients.Open(filepath.Join(cfg.Crypto.DataDir, "clients.enc"), enc)
	if err != nil {
		log.Fatalf("server: open clients store: %v", err)
	}

	tokenStore, err := auth.NewSealedTokenStore(filepath.Join(cfg.Crypto.DataDir, "tokens.enc"), enc)
	if err != nil {
		log.Fatalf("server: open token store: %v", err)
	}
	if err := ts.SetPersister(tokenStore); err != nil {
		log.Fatalf("server: attach token persister: %v", err)
	}

	adminStore, err := admins.Open(filepath.Join(cfg.Crypto.DataDir, "admins.enc"), enc)
	if err != nil {
		log.Fatalf("server: open admin store: %v", err)
	}

	session := auth.NewSessionStrategy(12 * time.Hour)
	ap.Register(session)

	s := &Server{
		cfg:             cfg,
		git:             gitmanager.NewManager(cfg.Storage.RepoRoot),
		auth:            ap,
		tokenStrategy:   ts,
		sessionStrategy: session,
		encryptor:       enc,
		clients:         clientStore,
		admins:          adminStore,
		policy:          policy.TokenRepoPolicy{},
		mux:             http.NewServeMux(),
	}
	s.routes()
	return s
}

// SetPolicy replaces the authorisation evaluator. Used by tests and future
// per-deployment configuration.
func (s *Server) SetPolicy(p policy.Evaluator) { s.policy = p }

// Admins returns the admin store (used by CLI tools like --add-admin).
func (s *Server) Admins() *admins.Store { return s.admins }

// Clients returns the enrolled-clients store.
func (s *Server) Clients() *clients.Store { return s.clients }

// Encryptor returns the server's NaCl Encryptor (used by tests and external
// management CLIs).
func (s *Server) Encryptor() *crypto.Encryptor { return s.encryptor }

// Auth returns the auth provider for registration of strategies.
func (s *Server) Auth() *auth.Provider {
	return s.auth
}

// TokenStrategy returns the token strategy for external token management.
func (s *Server) TokenStrategy() *auth.TokenStrategy {
	return s.tokenStrategy
}

// Handler returns the HTTP handler chain (sealed-body middleware → auth
// middleware → mux) for use in tests and Start().
func (s *Server) Handler() http.Handler {
	return s.sealedMiddleware(s.auth.Middleware(s.mux))
}

// Start begins listening for HTTP requests and blocks until SIGINT/SIGTERM
// triggers a graceful shutdown. The listening socket is always released on
// exit so a restart never trips over a stale port.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("port %d already in use — inspect with: lsof -iTCP:%d -sTCP:LISTEN", s.cfg.Server.Port, s.cfg.Server.Port)
		}
		return err
	}

	httpSrv := &http.Server{Handler: s.Handler()}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	shutdownErr := make(chan error, 1)
	go func() {
		sig := <-sigCh
		log.Printf("server: received %s, shutting down...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr <- httpSrv.Shutdown(ctx)
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Println("server: stopped cleanly")
	return nil
}

// routes registers all HTTP handlers.
func (s *Server) routes() {
	// Pages
	s.mux.HandleFunc("/", s.handleIndex)

	// Swagger
	s.mux.Handle("/swagger/", httpSwagger.WrapHandler)

	// Static assets (embedded logo etc.)
	s.mux.HandleFunc("/assets/", s.handleAssets)

	// API
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/repos", s.handleRepos)
	s.mux.HandleFunc("/api/repos/", s.handleRepoRouter)
	s.mux.HandleFunc("/api/auth/token", s.handleToken)
	s.mux.HandleFunc("/api/crypto/pubkey", s.handleServerPubKey)
	s.mux.HandleFunc("/api/clients/enroll", s.handleEnroll)

	// Admin UI + session endpoints
	s.mux.HandleFunc("/admin", s.handleAdminPage)
	s.mux.HandleFunc("/admin/", s.handleAdminPage)
	s.mux.HandleFunc("/admin/login", s.handleAdminLogin)
	s.mux.HandleFunc("/admin/logout", s.handleAdminLogout)
	s.mux.HandleFunc("/api/admin/session", s.handleAdminSession)
	s.mux.HandleFunc("/api/admin/tokens", s.handleAdminTokens)

	// Git smart HTTP transport
	s.mux.HandleFunc("/git/", s.handleGitRouter)
}

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
