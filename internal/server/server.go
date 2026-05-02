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
	"sync"
	"syscall"
	"time"

	"path/filepath"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/audit"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/auth/oauth"
	"github.com/petervdpas/GiGot/internal/clients"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/crypto"
	"github.com/petervdpas/GiGot/internal/destinations"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
	"github.com/petervdpas/GiGot/internal/tags"

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
	accounts        *accounts.Store
	credentials     *credentials.Store
	destinations    *destinations.Store
	tags            *tags.Store
	systemAudit     *audit.SystemLog
	policy          policy.Evaluator
	mux             *http.ServeMux

	// version is the build-stamped version string passed in by the
	// CLI entry point via SetVersion. Empty in tests where main()
	// isn't involved; templates skip the suffix when empty so the
	// brand strip falls back to "GiGot" alone. Single source of
	// truth for every place the UI shows "GiGot vX.Y.Z" — the
	// landing page, login card, register page, and JS-rendered
	// admin sidebar all read this through one helper.
	version string

	// authMu guards every field touched by the /admin/auth hot-reload
	// path: cfg.Auth (AllowLocal + nested OAuth / Gateway blocks),
	// oauthProviders (the Registry itself mutates in place via
	// Replace/Remove; the pointer is stable, but its contents change
	// here), and gatewayStrategy (pointer swap). Callers hold a read
	// lock while inspecting these; ReloadAuth holds the write lock
	// for the swap + persist.
	authMu sync.RWMutex

	// Phase-3 OAuth/OIDC. Nil when no provider is enabled —
	// handleOAuthLogin 404s in that case, same as if the route
	// weren't registered at all.
	oauthProviders *oauth.Registry
	oauthState     *oauth.StateStore

	// Phase-4 signed-header gateway strategy. Nil when
	// cfg.Auth.Gateway.Enabled=false; non-nil when a trusted fronting
	// proxy is forwarding identity claims. Consulted by
	// requireAdminSession so gateway admins reach the UI without a
	// session cookie. See docs/design/accounts.md §9.
	gatewayStrategy *gatewayStrategy

	// pushDest fires one outbound mirror push. Injected so tests can
	// stub the shell-out without running real git against a real remote.
	pushDest pushDestinationFn

	// mirrorWorker is the post-receive fan-out queue (slice 2b). After
	// every accepted client push, the receive-pack handler enqueues
	// the repo name; the worker then fires one push per enabled
	// destination. Optional so tests that need deterministic behavior
	// can swap or disable it.
	mirrorWorker *mirrorWorker
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
	ap.MarkPublic("/api/register")         // self-service registration (accounts.md §7)
	ap.MarkPublic("/api/admin/session")    // returns 401 internally for the page to decide
	ap.MarkPublic("/admin")
	ap.MarkPublic("/admin/")
	ap.MarkPublic("/signin")
	ap.MarkPublic("/signin/")
	ap.MarkPublic("/admin/login")
	ap.MarkPublic("/admin/logout")
	ap.MarkPublic("/admin/register")       // self-service registration page
	ap.MarkPublicPrefix("/admin/login/")   // OAuth redirect + callback (Phase 3)
	ap.MarkPublic("/api/admin/providers")  // enabled OAuth providers, public to the login page
	ap.MarkPublic("/admin/credentials")
	ap.MarkPublic("/admin/credentials/")
	ap.MarkPublic("/admin/tags")
	ap.MarkPublic("/admin/tags/")
	ap.MarkPublic("/admin/accounts")
	ap.MarkPublic("/admin/accounts/")
	ap.MarkPublic("/admin/auth")
	ap.MarkPublic("/admin/auth/")
	ap.MarkPublic("/user")
	ap.MarkPublic("/user/")
	ap.MarkPublic("/help")
	ap.MarkPublicPrefix("/help/")
	ap.MarkPublicPrefix("/swagger/")
	ap.MarkPublicPrefix("/assets/")
	// Basic auth is only meaningful for /git/* — git-the-binary can't
	// send Bearer. Everywhere else, Bearer-only (tightened defence in
	// depth; the middleware rejects Basic headers outside this prefix
	// with a 401 + WWW-Authenticate: Bearer).
	ap.MarkBasicPrefix("/git/")

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

	accountStore, err := accounts.Open(
		filepath.Join(cfg.Crypto.DataDir, "accounts.enc"),
		filepath.Join(cfg.Crypto.DataDir, "admins.enc"), // legacy, auto-migrated on first boot
		enc,
	)
	if err != nil {
		log.Fatalf("server: open accounts store: %v", err)
	}
	if err := seedAdmins(accountStore, cfg.Admins); err != nil {
		log.Fatalf("server: seed admins: %v", err)
	}
	warnPasswordlessLocalAdmins(accountStore)
	warnLockoutRisk(cfg, accountStore)

	credentialStore, err := credentials.Open(filepath.Join(cfg.Crypto.DataDir, "credentials.enc"), enc)
	if err != nil {
		log.Fatalf("server: open credential store: %v", err)
	}

	destinationStore, err := destinations.Open(filepath.Join(cfg.Crypto.DataDir, "destinations.enc"), enc)
	if err != nil {
		log.Fatalf("server: open destination store: %v", err)
	}

	tagStore, err := tags.Open(filepath.Join(cfg.Crypto.DataDir, "tags.enc"), enc)
	if err != nil {
		log.Fatalf("server: open tag store: %v", err)
	}

	systemAudit, err := audit.Open(filepath.Join(cfg.Crypto.DataDir, "audit_system.enc"), enc)
	if err != nil {
		log.Fatalf("server: open system audit log: %v", err)
	}

	session := auth.NewSessionStrategy(12 * time.Hour)
	sessionStore, err := auth.NewSealedSessionStore(filepath.Join(cfg.Crypto.DataDir, "sessions.enc"), enc)
	if err != nil {
		log.Fatalf("server: open session store: %v", err)
	}
	if err := session.SetPersister(sessionStore); err != nil {
		log.Fatalf("server: attach session persister: %v", err)
	}
	ap.Register(session)

	// Phase-4 gateway: resolve the HMAC secret from the vault, wire a
	// Verifier, register the strategy after session so bearer + cookie
	// still win when they're present. A misconfigured gateway block
	// fails boot so an operator sees the problem before the first
	// proxy-forwarded request arrives. See docs/design/accounts.md §9.
	gwStrategy, err := buildGatewayStrategy(cfg.Auth.Gateway, credentialStore, accountStore)
	if err != nil {
		log.Fatalf("server: build gateway strategy: %v", err)
	}
	if gwStrategy != nil {
		ap.Register(gwStrategy)
		log.Printf("server: gateway strategy enabled (header=%q, allow_register=%v)",
			cfg.Auth.Gateway.UserHeader, cfg.Auth.Gateway.AllowRegister)
	}

	// Phase-3: resolve each enabled OAuth provider against the
	// credential vault for its client_secret_ref, run OIDC discovery
	// where relevant, and hand the handler a read-only registry. A
	// broken provider (unresolvable secret, unreachable discovery URL,
	// empty client_id) fails boot so the operator sees the problem
	// before the first user clicks the button.
	oauthRegistry, err := oauth.Build(
		context.Background(),
		cfg.Auth.OAuth,
		func(name string) (string, error) {
			cred, err := credentialStore.Get(name)
			if err != nil {
				return "", err
			}
			return cred.Secret, nil
		},
	)
	if err != nil {
		log.Fatalf("server: build oauth providers: %v", err)
	}

	s := &Server{
		cfg:             cfg,
		git:             gitmanager.NewManager(cfg.Storage.RepoRoot),
		auth:            ap,
		tokenStrategy:   ts,
		sessionStrategy: session,
		encryptor:       enc,
		clients:         clientStore,
		accounts:        accountStore,
		credentials:     credentialStore,
		destinations:    destinationStore,
		tags:            tagStore,
		systemAudit:     systemAudit,
		policy:          policy.TokenRepoPolicy{},
		mux:             http.NewServeMux(),
		pushDest:        executeMirrorPush,
		oauthProviders:  oauthRegistry,
		oauthState:      oauth.NewStateStore(10 * time.Minute),
		gatewayStrategy: gwStrategy,
	}
	// Wire the mirror worker. listDests / getCred close over the stores;
	// fireOne closes over the server so it can reuse the same syncOnce
	// code the manual Sync-now handler calls. That way the two paths
	// write last_sync_* identically and we have one push recording
	// surface, not two.
	s.mirrorWorker = newMirrorWorker(
		func(repo string) []*destinations.Destination {
			return s.destinations.All(repo)
		},
		func(name string) (*credentials.Credential, error) {
			return s.credentials.Get(name)
		},
		func(ctx context.Context, repo string, dest *destinations.Destination, cred *credentials.Credential) (*destinations.Destination, error) {
			return s.syncOnce(ctx, repo, dest, cred)
		},
	)
	// Retro-install the refs/audit/* pre-receive guard on any repos
	// created before slice 2 shipped. Newly-created repos get it in
	// InitBare/CloneBare. Logged but not fatal — a running server with
	// some unguarded legacy repos is still strictly better than
	// failing to boot.
	if err := s.git.EnsureAuditGuards(); err != nil {
		log.Printf("server: EnsureAuditGuards: %v", err)
	}
	// Back-fill refs/audit/main for any repo that has commits but an
	// empty audit ref — closes the gap for repos that existed before
	// audit was enabled or were cloned in before the backfill-on-create
	// path landed. Idempotent: repos with audit history are skipped.
	if err := s.git.BackfillAuditForAll(); err != nil {
		log.Printf("server: BackfillAuditForAll: %v", err)
	}
	s.routes()
	return s
}

// SetPolicy replaces the authorisation evaluator. Used by tests and future
// per-deployment configuration.
func (s *Server) SetPolicy(p policy.Evaluator) { s.policy = p }

// allowLocal is the read-locked accessor for cfg.Auth.AllowLocal.
// Handlers that gate on the local-password path use this so a
// concurrent ReloadAuth can't tear a single bool read into a
// partially-updated value.
func (s *Server) allowLocal() bool {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return s.cfg.Auth.AllowLocal
}

// ReloadAuth swaps the auth-runtime state (allow_local, OAuth
// registry contents, gateway strategy) to match a new AuthConfig.
// Either the whole swap succeeds — new OAuth providers discover
// cleanly, gateway secret resolves, verifier builds — or nothing
// changes and the caller gets the build error. On success, the new
// state is also persisted to the config file this server was loaded
// from (if any), so a restart picks up the same shape.
//
// Callers: /api/admin/auth PATCH. Not intended for general use;
// holding the write lock blocks every login + middleware hit for
// its duration, which is bounded (config parse + discovery
// round-trips) but non-zero.
func (s *Server) ReloadAuth(newCfg config.AuthConfig) error {
	resolver := func(name string) (string, error) {
		cred, err := s.credentials.Get(name)
		if err != nil {
			return "", err
		}
		return cred.Secret, nil
	}

	// Build candidates outside the lock: discovery RPCs + secret
	// lookups shouldn't block every in-flight auth check. If any
	// build fails, nothing has changed yet — the existing strategies
	// keep running and the caller sees the error.
	newRegistry, err := oauth.Build(context.Background(), newCfg.OAuth, resolver)
	if err != nil {
		return fmt.Errorf("oauth: %w", err)
	}
	newGateway, err := buildGatewayStrategy(newCfg.Gateway, s.credentials, s.accounts)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()

	// OAuth: swap the Registry pointer. In-flight handleOAuthLogin
	// readers that already captured the old pointer finish against
	// the old providers (safe — the old Registry is immutable after
	// this pointer update drops its last reference).
	s.oauthProviders = newRegistry

	// Gateway strategy slot: register / replace / remove in the
	// auth.Provider chain so the middleware sees the update on its
	// next snapshot. Replace returns false when the slot was empty
	// (first-ever enable of the gateway at runtime).
	switch {
	case newGateway != nil:
		if !s.auth.Replace(newGateway) {
			s.auth.Register(newGateway)
		}
	case s.gatewayStrategy != nil:
		s.auth.Remove("gateway")
	}
	s.gatewayStrategy = newGateway

	// AuthConfig must move last — the guarded reads in handlers see
	// either the old strategies + old allow_local or the new pair,
	// never a half-updated mix.
	s.cfg.Auth = newCfg

	// Persist so a restart inherits the change. Skipping when path
	// is empty (tests, ad-hoc in-process servers) is intentional —
	// ReloadAuth is still useful there for verifying swap semantics
	// without requiring a real file on disk.
	if s.cfg.Path != "" {
		if err := s.cfg.Save(s.cfg.Path); err != nil {
			log.Printf("server: ReloadAuth: persist to %s: %v", s.cfg.Path, err)
			return fmt.Errorf("persist: %w", err)
		}
	}
	return nil
}

// Accounts returns the account store (used by CLI tools like the demo
// bootstrap and future `gigot admin set-password`).
func (s *Server) Accounts() *accounts.Store { return s.accounts }

// seedAdmins upserts bootstrap admin entries from cfg.Admins into the
// account store, creating any that are missing with role=admin. Never
// overwrites an existing account — the store wins once it has data
// (Phase 2's /register flow and admin-UI role changes write there).
func seedAdmins(store *accounts.Store, seeds []config.AdminSeed) error {
	for _, s := range seeds {
		if store.Has(s.Provider, s.Identifier) {
			continue
		}
		if _, err := store.Put(accounts.Account{
			Provider:    s.Provider,
			Identifier:  s.Identifier,
			Role:        accounts.RoleAdmin,
			DisplayName: s.DisplayName,
		}); err != nil {
			return fmt.Errorf("seed %s:%s: %w", s.Provider, s.Identifier, err)
		}
	}
	return nil
}

// warnLockoutRisk logs a loud notice when auth.allow_local=false is
// set but no non-local path can actually admit an admin — a
// combination that silently locks admins out at the next restart.
// Surfacing it here is the Phase-5 safety rail: the documented
// default flips to false, and the runtime must notice operators who
// flip the flag without configuring OAuth/gateway first.
func warnLockoutRisk(cfg *config.Config, store *accounts.Store) {
	if cfg.Auth.AllowLocal {
		return
	}
	nonLocalAdmins := 0
	for _, a := range store.List() {
		if a.Role == accounts.RoleAdmin && a.Provider != accounts.ProviderLocal {
			nonLocalAdmins++
		}
	}
	anyOAuth := cfg.Auth.OAuth.GitHub.Enabled ||
		cfg.Auth.OAuth.Entra.Enabled ||
		cfg.Auth.OAuth.Microsoft.Enabled
	anyGateway := cfg.Auth.Gateway.Enabled
	if !anyOAuth && !anyGateway {
		log.Printf("server: WARNING: auth.allow_local=false but no gateway or OAuth provider is enabled — admin HTTP access is impossible. Re-enable allow_local, or configure auth.gateway / auth.oauth.*.")
		return
	}
	if nonLocalAdmins == 0 {
		log.Printf("server: WARNING: auth.allow_local=false and no non-local admin account exists — admins must register via the configured provider(s) before the next restart or they'll be locked out.")
	}
}

// warnPasswordlessLocalAdmins logs a loud notice for any local-provider
// admin account with no password hash set. Common fresh-install case:
// the default seed creates local:admin but no password is set yet, so
// the /admin/login form can't succeed until the operator runs a
// password-setting command (demo CLI or a future
// `gigot admin set-password`). Silent failure here is worse than
// noise; people notice when they can't log in anyway.
func warnPasswordlessLocalAdmins(store *accounts.Store) {
	for _, a := range store.List() {
		if a.Provider == accounts.ProviderLocal && a.Role == accounts.RoleAdmin && a.PasswordHash == "" {
			log.Printf("server: local admin %q has no password set — login will fail until one is configured", a.Identifier)
		}
	}
}

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

// SetVersion stamps the build-time version onto the server so brand
// strips (landing page, login, register, admin sidebar) can render
// "GiGot vX.Y.Z". Called once from cli.Execute after server.New;
// tests don't call it and templates fall back to "GiGot" alone when
// empty. Idempotent — re-stamping is harmless.
func (s *Server) SetVersion(v string) {
	s.version = v
}

// brandVersion is the single rendering rule for the version suffix.
// Empty input → empty output (skip suffix); non-empty → "v" + value
// so a stamped "0.1.0" reads as "v0.1.0" in the UI. Every brand-strip
// surface — server-rendered templates and the meta tag the admin JS
// reads — flows through here so the prefix and missing-version
// behaviour can never drift between surfaces.
func (s *Server) brandVersion() string {
	if s.version == "" {
		return ""
	}
	return "v" + s.version
}

// pageData is the shared template-data shape for every page that
// renders the GiGot brand strip. Handlers that need extra fields
// (e.g. the index page's port + repo count) embed PageData on their
// own struct so the brand fields stay in lockstep across surfaces
// without each handler hand-rolling them.
type PageData struct {
	Version string
}

// pageData returns the brand context for any template that needs it.
// One source of truth feeding every handler.
func (s *Server) pageData() PageData {
	return PageData{Version: s.brandVersion()}
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
	s.mux.HandleFunc("/api/register", s.handleRegister)
	s.mux.HandleFunc("/api/crypto/pubkey", s.handleServerPubKey)
	s.mux.HandleFunc("/api/clients/enroll", s.handleEnroll)

	// Admin UI + session endpoints. /admin is the login card; the three
	// authenticated sections each live on their own peer URL.
	s.mux.HandleFunc("/admin", s.handleAdminPage)
	s.mux.HandleFunc("/admin/", s.handleAdminPage)
	// /signin is a friendlier alias for the same login card. /admin
	// stays canonical so OAuth callbacks, login.js, and tests don't
	// move; the landing page just points users at /signin.
	s.mux.HandleFunc("/signin", s.handleAdminPage)
	s.mux.HandleFunc("/signin/", s.handleAdminPage)
	s.mux.HandleFunc("/admin/login", s.handleAdminLogin)
	s.mux.HandleFunc("/admin/logout", s.handleAdminLogout)
	s.mux.HandleFunc("/admin/register", s.handleRegisterPage)
	// /admin/login/<provider>[/callback] — Phase-3 OAuth flow. Single
	// handler dispatches on the provider-name segment.
	s.mux.HandleFunc("/admin/login/", s.handleOAuthLogin)
	s.mux.HandleFunc("/api/admin/providers", s.handleOAuthProviders)
	s.mux.HandleFunc("/admin/repositories", s.adminPageHandler(repositoriesTmpl, "/admin/repositories", "/admin/repositories/"))
	s.mux.HandleFunc("/admin/repositories/", s.adminPageHandler(repositoriesTmpl, "/admin/repositories", "/admin/repositories/"))
	s.mux.HandleFunc("/admin/subscriptions", s.adminPageHandler(subscriptionsTmpl, "/admin/subscriptions", "/admin/subscriptions/"))
	s.mux.HandleFunc("/admin/subscriptions/", s.adminPageHandler(subscriptionsTmpl, "/admin/subscriptions", "/admin/subscriptions/"))
	s.mux.HandleFunc("/admin/credentials", s.handleCredentialsPage)
	s.mux.HandleFunc("/admin/credentials/", s.handleCredentialsPage)
	s.mux.HandleFunc("/admin/tags", s.handleTagsPage)
	s.mux.HandleFunc("/admin/tags/", s.handleTagsPage)
	s.mux.HandleFunc("/admin/accounts", s.handleAccountsPage)
	s.mux.HandleFunc("/admin/accounts/", s.handleAccountsPage)
	s.mux.HandleFunc("/admin/auth", s.handleAuthPage)
	s.mux.HandleFunc("/admin/auth/", s.handleAuthPage)
	s.mux.HandleFunc("/user", s.handleUserPage)
	s.mux.HandleFunc("/user/", s.handleUserPage)
	// /help and /help/<slug> render embedded markdown via goldmark.
	// Public so an operator can reach it without a session.
	s.mux.HandleFunc("/help", s.handleHelp)
	s.mux.HandleFunc("/help/", s.handleHelp)
	s.mux.HandleFunc("/api/me", s.handleMe)
	s.mux.HandleFunc("/api/admin/session", s.handleAdminSession)
	s.mux.HandleFunc("/api/admin/tokens", s.handleAdminTokens)
	s.mux.HandleFunc("/api/admin/tokens/bind", s.handleAdminBindToken)
	s.mux.HandleFunc("/api/admin/credentials", s.handleAdminCredentials)
	s.mux.HandleFunc("/api/admin/credentials/", s.handleAdminCredential)
	s.mux.HandleFunc("/api/admin/tags", s.handleAdminTags)
	s.mux.HandleFunc("/api/admin/tags/", s.handleAdminTag)
	s.mux.HandleFunc("/api/admin/accounts", s.handleAdminAccounts)
	s.mux.HandleFunc("/api/admin/accounts/", s.handleAdminAccount)
	s.mux.HandleFunc("/api/admin/auth", s.handleAdminAuth)
	// Admin per-repo subroutes live under /api/admin/repos/{name}/...:
	//   /destinations[/{id}] — mirror-sync targets
	//   /formidable          — convert plain repo to a Formidable context
	// A single prefix handler sniffs the suffix and dispatches.
	s.mux.HandleFunc("/api/admin/repos/", s.handleAdminRepoSub)

	// Git smart HTTP transport
	s.mux.HandleFunc("/git/", s.handleGitRouter)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	repos, _ := s.git.List()

	// Embeds PageData so the brand strip on the landing page reads
	// the same Version field as every other template surface. Repo
	// root + Go version were dropped — operator-facing detail with
	// no actionable meaning to a user, and Go version on a public
	// page is a small fingerprinting gift to vuln scanners.
	data := struct {
		PageData
		Port      int
		RepoCount int
	}{
		PageData:  s.pageData(),
		Port:      s.cfg.Server.Port,
		RepoCount: len(repos),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	indexTmpl.Execute(w, data)
}
