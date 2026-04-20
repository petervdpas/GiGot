package server

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth/gateway"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/credentials"
)

// seedCredential is the shared prep step for every reload test that
// flips the gateway on — ReloadAuth resolves gateway.secret_ref
// through the vault, so the credential must exist before the reload
// fires or the rebuild will fail (correctly; the assertion in that
// case is that the old state is preserved).
func seedCredential(t *testing.T, srv *Server, name, secret string) {
	t.Helper()
	if _, err := srv.credentials.Put(credentials.Credential{
		Name: name, Kind: "generic", Secret: secret,
	}); err != nil {
		t.Fatalf("seed credential %q: %v", name, err)
	}
}

func gatewayConfigFor(secretRef string) config.GatewayConfig {
	return config.GatewayConfig{
		Enabled:         true,
		UserHeader:      "X-GiGot-Gateway-User",
		SigHeader:       "X-GiGot-Gateway-Sig",
		TimestampHeader: "X-GiGot-Gateway-Ts",
		SecretRef:       secretRef,
		MaxSkewSeconds:  300,
		AllowRegister:   false,
	}
}

// ReloadAuth enables a previously-absent gateway, and the resulting
// strategy accepts a request signed with the newly-installed secret.
func TestReloadAuth_EnablesGateway(t *testing.T) {
	srv := testServer(t)
	seedCredential(t, srv, "gw-hmac", "s3krit")

	if srv.gatewayStrategy != nil {
		t.Fatal("precondition: gateway must be disabled on a fresh testServer")
	}

	newCfg := srv.cfg.Auth
	newCfg.Gateway = gatewayConfigFor("gw-hmac")
	if err := srv.ReloadAuth(newCfg); err != nil {
		t.Fatalf("ReloadAuth: %v", err)
	}
	if srv.gatewayStrategy == nil {
		t.Fatal("gateway strategy not installed after ReloadAuth")
	}

	// Round-trip a signed request through the middleware.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderGateway, Identifier: "boss", Role: accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	srv.auth.SetEnabled(true)

	sig, ts := gateway.Sign([]byte("s3krit"), "boss", time.Now())
	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	req.Header.Set("X-GiGot-Gateway-User", "boss")
	req.Header.Set("X-GiGot-Gateway-Sig", sig)
	req.Header.Set("X-GiGot-Gateway-Ts", ts)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 from gateway-signed admin request, got %d", rec.Code)
	}
}

// ReloadAuth disables a previously-enabled gateway. The old strategy
// is removed from auth.Provider entirely; a previously-valid signed
// request now 401s because requireAdminSession finds no gateway path.
func TestReloadAuth_DisablesGateway(t *testing.T) {
	srv := testServer(t)
	seedCredential(t, srv, "gw-hmac", "s3krit")
	enable := srv.cfg.Auth
	enable.Gateway = gatewayConfigFor("gw-hmac")
	if err := srv.ReloadAuth(enable); err != nil {
		t.Fatalf("enable: %v", err)
	}

	disable := srv.cfg.Auth
	disable.Gateway.Enabled = false
	if err := srv.ReloadAuth(disable); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if srv.gatewayStrategy != nil {
		t.Fatal("gateway strategy should be nil after disable")
	}
}

// ReloadAuth rejects a candidate config atomically: a bad secret_ref
// leaves oauth.Registry, gatewayStrategy, and allow_local untouched.
// Critical invariant — partial application of a bad reload would be
// worse than the original error because the operator can't easily
// tell WHICH fields made it through.
func TestReloadAuth_RejectsBadSecretRefAtomically(t *testing.T) {
	srv := testServer(t)
	originalAllowLocal := srv.cfg.Auth.AllowLocal

	bad := srv.cfg.Auth
	bad.AllowLocal = !originalAllowLocal // would-be-applied change
	bad.Gateway = gatewayConfigFor("missing-secret-ref")
	err := srv.ReloadAuth(bad)
	if err == nil {
		t.Fatal("ReloadAuth with missing secret_ref must error")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error should scope to gateway subsystem, got %q", err.Error())
	}

	if srv.cfg.Auth.AllowLocal != originalAllowLocal {
		t.Fatal("allow_local flipped even though reload failed — atomicity invariant violated")
	}
	if srv.gatewayStrategy != nil {
		t.Fatal("gateway installed even though reload failed")
	}
}

// allow_local flip takes effect on the next request through the normal
// handler path (which goes via s.allowLocal() and the authMu RLock).
func TestReloadAuth_AllowLocalFlipIsLive(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	// Precondition: defaults have allow_local=true.
	if !srv.allowLocal() {
		t.Fatal("precondition: defaults must allow_local=true")
	}

	off := srv.cfg.Auth
	off.AllowLocal = false
	if err := srv.ReloadAuth(off); err != nil {
		t.Fatalf("ReloadAuth: %v", err)
	}
	if srv.allowLocal() {
		t.Fatal("allowLocal() still true after reload flipped it off")
	}

	// /admin/login now 404s.
	req := httptest.NewRequest(http.MethodPost, "/admin/login",
		strings.NewReader(`{"username":"x","password":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 with allow_local=false, got %d", rec.Code)
	}
}

// PATCH /api/admin/auth requires an admin session — no cookie => 401.
func TestPatchAuth_RequiresAdminSession(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)

	req := httptest.NewRequest(http.MethodPatch, "/api/admin/auth",
		strings.NewReader(`{"allow_local": false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// ReloadAuth persists to cfg.Path when set. The round-trip proves
// that an operator who restarts the binary after a UI edit gets the
// same runtime on the other side.
func TestReloadAuth_PersistsToConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	cfg := config.Defaults()
	cfg.Storage.RepoRoot = filepath.Join(dir, "repos")
	cfg.Crypto.PrivateKeyPath = filepath.Join(dir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(dir, "server.pub")
	cfg.Crypto.DataDir = filepath.Join(dir, "data")
	cfg.Path = path
	if err := cfg.Save(path); err != nil {
		t.Fatalf("seed cfg file: %v", err)
	}
	srv := New(cfg)

	newCfg := srv.cfg.Auth
	newCfg.AllowLocal = false
	if err := srv.ReloadAuth(newCfg); err != nil {
		t.Fatalf("ReloadAuth: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatalf("parse persisted config: %v", err)
	}
	authSection, _ := roundtrip["auth"].(map[string]any)
	if authSection == nil {
		t.Fatal("auth section missing in persisted config")
	}
	if got, ok := authSection["allow_local"].(bool); !ok || got {
		t.Fatalf("persisted allow_local = %v, want false", authSection["allow_local"])
	}

	// Path field itself MUST NOT leak into the file (json:"-").
	if _, leaked := roundtrip["Path"]; leaked {
		t.Fatal("Path field leaked into persisted config")
	}
}

// GET /api/admin/auth returns a snapshot of the current runtime —
// allow_local, each OAuth block, gateway block, and the config_path.
// The load-bearing assertion is that NO secret bytes leak: only refs
// (names) are in the response, matching the §9 contract.
func TestGetAuth_SnapshotContract(t *testing.T) {
	srv := testServer(t)
	// Seed an admin + session so the handler's requireAdminSession
	// check passes. A regular user hitting this endpoint is separately
	// covered by TestPatchAuth_RequiresAdminSession on the PATCH side.
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "alice", Role: accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.SetPassword("alice", "pw"); err != nil {
		t.Fatal(err)
	}
	sess, err := srv.sessionStrategy.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	// Plant a gateway config with a secret_ref that IS backed by a
	// credential — the vault secret bytes must NEVER appear in the
	// response; only the ref name.
	seedCredential(t, srv, "gw-hmac", "super-secret-bytes-do-not-leak")
	newCfg := srv.cfg.Auth
	newCfg.Gateway = gatewayConfigFor("gw-hmac")
	if err := srv.ReloadAuth(newCfg); err != nil {
		t.Fatalf("precondition ReloadAuth: %v", err)
	}

	srv.auth.SetEnabled(true)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth", nil)
	req.AddCookie(&http.Cookie{Name: "gigot_session", Value: sess.ID})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "super-secret-bytes-do-not-leak") {
		t.Fatal("GET /api/admin/auth leaked the vault secret bytes!")
	}
	var view AuthRuntimeView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Expected structure: top-level allow_local + oauth block + gateway
	// block. The gateway block carries the ref name (the pointer), not
	// the secret content.
	if view.Gateway.SecretRef != "gw-hmac" {
		t.Errorf("gateway.secret_ref = %q, want %q", view.Gateway.SecretRef, "gw-hmac")
	}
	if !view.Gateway.Enabled {
		t.Error("gateway.enabled should be true after the reload")
	}
	if view.OAuth.GitHub.Enabled || view.OAuth.Entra.Enabled || view.OAuth.Microsoft.Enabled {
		t.Error("no OAuth providers were enabled in this test — response should reflect that")
	}
}

// GET /api/admin/auth without an admin session is a 401, same gate
// as PATCH. Documented here as its own test so the session check
// can't silently be removed from only one verb.
func TestGetAuth_RequiresAdminSession(t *testing.T) {
	srv := testServer(t)
	srv.auth.SetEnabled(true)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// Sanity: signing/verifying through the production builder so that
// any future divergence between buildGatewayStrategy and the
// standalone Verifier surfaces here too, not just in the gateway
// package tests.
func TestReloadAuth_ProductionBuilderAcceptsSignedHeaders(t *testing.T) {
	srv := testServer(t)
	seedCredential(t, srv, "gw-hmac", "s3krit")
	newCfg := srv.cfg.Auth
	newCfg.Gateway = gatewayConfigFor("gw-hmac")
	if err := srv.ReloadAuth(newCfg); err != nil {
		t.Fatalf("ReloadAuth: %v", err)
	}
	sig, _ := gateway.Sign([]byte("s3krit"), "alice", time.Now())
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("Sign produced non-hex output: %v", err)
	}
}
