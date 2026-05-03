package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// Config is the top-level GiGot configuration.
type Config struct {
	Server  ServerConfig  `json:"server"`
	Storage StorageConfig `json:"storage"`
	Auth    AuthConfig    `json:"auth"`
	Crypto  CryptoConfig  `json:"crypto"`
	Logging LoggingConfig `json:"logging"`
	Limits  LimitsConfig  `json:"limits"`
	Mirror  MirrorConfig  `json:"mirror"`
	// Admins is the bootstrap seed list of admin accounts — NOT a
	// live allowlist. On startup, entries not yet in the accounts
	// store are upserted with role=admin; after that the store is the
	// source of truth. See docs/design/accounts.md §4.
	Admins []AdminSeed `json:"admins"`

	// Path is the file this config was loaded from, remembered so
	// the /admin/auth hot-reload handler can persist in-place without
	// the operator re-supplying the path. Runtime-only; never
	// serialised (a Save(path) round-trip must not grow this field
	// into the file).
	Path string `json:"-"`
}

// AdminSeed is one entry in the bootstrap seed list. Role is implied
// (always admin) — regular accounts come via Phase 2's /register flow,
// not config. See docs/design/accounts.md §4.
type AdminSeed struct {
	Provider    string `json:"provider"`
	Identifier  string `json:"identifier"`
	DisplayName string `json:"display_name,omitempty"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// FormidableFirst flips the deployment-level stamping default per
	// docs/design/structured-sync-api.md §2.7. When true, both `POST
	// /api/repos` init and clone stamp .formidable/context.json by
	// default (clones idempotently). When false (the default), stamping
	// is strictly opt-in per request via scaffold_formidable: true.
	FormidableFirst bool `json:"formidable_first"`
}

// StorageConfig controls where repositories are kept.
type StorageConfig struct {
	RepoRoot string `json:"repo_root"`
}

// AuthConfig controls authentication.
type AuthConfig struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	// AllowLocal toggles the username+password login path for
	// local-provider accounts. When false, /api/admin/login returns
	// 404 and only non-local providers (gateway, OAuth) can
	// authenticate. CLI `--allow-local=true|false` on `gigot serve`
	// overrides this value for the invocation. See
	// docs/design/accounts.md §4.
	AllowLocal bool `json:"allow_local"`
	// OAuth configures the Phase-3 redirect-flow providers. Each entry
	// is independent; enable one, two, or three. See
	// docs/design/accounts.md §8.
	OAuth OAuthConfig `json:"oauth"`
	// Gateway configures the Phase-4 signed-header strategy for a
	// trusted fronting proxy. When enabled, every request carrying a
	// valid HMAC-signed identity header is treated as authenticated.
	// See docs/design/accounts.md §9.
	Gateway GatewayConfig `json:"gateway"`
}

// OAuthConfig holds per-provider OIDC / OAuth2 settings. The three
// providers are intentionally separate (§2 of the design doc): entra
// (tenant-scoped work/school), microsoft (consumer MSA), and github
// (OAuth, no OIDC discovery). Leaving a block out or setting
// Enabled=false keeps that provider dark.
type OAuthConfig struct {
	GitHub    OAuthProviderConfig `json:"github"`
	Entra     OAuthProviderConfig `json:"entra"`
	Microsoft OAuthProviderConfig `json:"microsoft"`
}

// OAuthProviderConfig is one enabled redirect-flow provider.
// ClientSecretRef names a credential in the existing vault (the OAuth
// client secret is not stored in the config file; the vault already
// seals secrets at rest). TenantID is only read for the entra
// provider; it's ignored elsewhere. AllowRegister controls what
// happens on first successful callback: true auto-creates a
// role=regular account for the verified claim, false rejects with a
// landing message ("ask an admin to register you").
type OAuthProviderConfig struct {
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"client_id"`
	ClientSecretRef string `json:"client_secret_ref"`
	TenantID        string `json:"tenant_id"`
	AllowRegister   bool   `json:"allow_register"`
	// DisplayName is the label shown on the login page's provider
	// button ("Sign in with <name>"). Optional; the provider key is
	// used if empty.
	DisplayName string `json:"display_name"`
}

// GatewayConfig is the Phase-4 signed-header identity strategy. A
// trusted fronting proxy (APIM, nginx+auth_request, oauth2-proxy,
// Envoy, etc.) authenticates the user, then forwards GiGot three
// headers: the identifier, a Unix timestamp, and an HMAC-SHA256
// signature over "<identifier>\n<timestamp>" keyed on a shared
// secret. GiGot verifies the signature and the timestamp skew, then
// resolves (provider=gateway, identifier=<user>) in the accounts
// store. See docs/design/accounts.md §9.
//
// Header names default to the GiGot-namespaced set but are
// configurable so deploys with an existing proxy convention can point
// at whatever headers that proxy already emits.
type GatewayConfig struct {
	Enabled bool `json:"enabled"`
	// UserHeader carries the verified identifier (email, oid, sub,
	// whatever the proxy standardised on). Case-insensitive per HTTP.
	UserHeader string `json:"user_header"`
	// SigHeader carries the hex-encoded HMAC-SHA256 signature over
	// "<user>\n<timestamp>".
	SigHeader string `json:"sig_header"`
	// TimestampHeader carries the Unix seconds timestamp. Rejects
	// replays older than MaxSkewSeconds.
	TimestampHeader string `json:"timestamp_header"`
	// SecretRef names a credential in the vault holding the shared
	// HMAC secret. Required when Enabled=true.
	SecretRef string `json:"secret_ref"`
	// MaxSkewSeconds bounds how far the timestamp can be from
	// time.Now() in either direction. Defaults to 300 (5 minutes).
	MaxSkewSeconds int `json:"max_skew_seconds"`
	// AllowRegister auto-creates a role=regular account on first
	// successful claim when no matching gateway account exists. When
	// false, unknown users get ErrNoCredentials and fall through to
	// the 401 path.
	AllowRegister bool `json:"allow_register"`
	// DisplayName is currently unused by any UI (the gateway is
	// transparent to the user), kept for symmetry with OAuth and for
	// log-line readability.
	DisplayName string `json:"display_name"`
}

// CryptoConfig controls server-side NaCl keypair storage and on-disk layout
// for encrypted data (client enrollments, token store).
type CryptoConfig struct {
	// PrivateKeyPath is where the server's private key is stored (base64, 0600).
	// Generated on first run if missing.
	PrivateKeyPath string `json:"private_key_path"`
	// PublicKeyPath is where the server's public key is stored (base64).
	PublicKeyPath string `json:"public_key_path"`
	// DataDir holds encrypted state files (clients.enc, tokens.enc, admins.json).
	DataDir string `json:"data_dir"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level string `json:"level"`
}

// MirrorConfig controls mirror-related background behaviours that
// don't belong on a per-request handler. StatusPollSec is the cadence
// at which the server re-checks each enabled destination's remote
// status via `git ls-remote` (so the admin UI's "in sync / diverged"
// badge stays fresh without an admin clicking Refresh on every
// destination). 0 disables the poller; the manual refresh button on
// the admin UI still works either way.
type MirrorConfig struct {
	StatusPollSec int `json:"status_poll_sec"`
}

// LimitsConfig controls server-side admission limits — concurrency
// caps and the retry-after hints they hand back to clients on
// rejection. PushSlots gates concurrent git-receive-pack handlers
// (the heavy write path); reads (clone / fetch / info-refs) are
// not slot-gated. PushRetryAfterSec is the integer-seconds value
// echoed in the `Retry-After` HTTP header on a 429 from the slot
// gate — clients that honor the header back off by that much.
// Editable hot-reload via PATCH /api/admin/limits.
type LimitsConfig struct {
	PushSlots         int `json:"push_slots"`
	PushRetryAfterSec int `json:"push_retry_after_sec"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 3417,
		},
		Storage: StorageConfig{
			RepoRoot: "./repos",
		},
		Auth: AuthConfig{
			Enabled:    false,
			Type:       "token",
			AllowLocal: true,
			Gateway: GatewayConfig{
				UserHeader:      "X-GiGot-Gateway-User",
				SigHeader:       "X-GiGot-Gateway-Sig",
				TimestampHeader: "X-GiGot-Gateway-Ts",
				MaxSkewSeconds:  300,
			},
		},
		Crypto: CryptoConfig{
			PrivateKeyPath: "./data/server.key",
			PublicKeyPath:  "./data/server.pub",
			DataDir:        "./data",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
		Limits: LimitsConfig{
			PushSlots:         10,
			PushRetryAfterSec: 5,
		},
		Mirror: MirrorConfig{
			// 10-minute default — see docs/design/remote-sync.md when
			// the design doc lands. Trade-off: smaller = fresher badge
			// + faster auth-failure detection; larger = less log noise
			// + fewer credential reads. Set to 0 to disable.
			StatusPollSec: 600,
		},
		Admins: []AdminSeed{
			{Provider: accounts.ProviderLocal, Identifier: "admin", DisplayName: "Primary admin"},
		},
	}
}

// normalizeAndValidateAdmins lowercases + trims each entry, rejects
// blank fields, unknown providers, and duplicate (provider, identifier)
// pairs. Mutates the slice to canonical form so downstream seeding can
// read it without re-normalising.
func normalizeAndValidateAdmins(entries []AdminSeed) ([]AdminSeed, error) {
	seen := make(map[string]struct{}, len(entries))
	out := make([]AdminSeed, 0, len(entries))
	for i, e := range entries {
		p := strings.ToLower(strings.TrimSpace(e.Provider))
		id := strings.ToLower(strings.TrimSpace(e.Identifier))
		if p == "" || id == "" {
			return nil, fmt.Errorf("admins[%d]: provider and identifier are required", i)
		}
		if !slices.Contains(accounts.KnownProviders, p) {
			return nil, fmt.Errorf("admins[%d]: unknown provider %q (known: %v)", i, e.Provider, accounts.KnownProviders)
		}
		key := p + "\x00" + id
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("admins[%d]: duplicate entry %s:%s", i, p, id)
		}
		seen[key] = struct{}{}
		out = append(out, AdminSeed{
			Provider:    p,
			Identifier:  id,
			DisplayName: strings.TrimSpace(e.DisplayName),
		})
	}
	return out, nil
}

// Load reads the config file. It looks for the path in this order:
//  1. Explicit path passed as argument (--config flag)
//  2. ./gigot.json in the working directory
//  3. Falls back to defaults
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if path == "" {
		path = "gigot.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == "gigot.json" {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Resolve relative paths against config file directory.
	dir := filepath.Dir(path)
	if !filepath.IsAbs(cfg.Storage.RepoRoot) {
		cfg.Storage.RepoRoot = filepath.Join(dir, cfg.Storage.RepoRoot)
	}
	if cfg.Crypto.PrivateKeyPath != "" && !filepath.IsAbs(cfg.Crypto.PrivateKeyPath) {
		cfg.Crypto.PrivateKeyPath = filepath.Join(dir, cfg.Crypto.PrivateKeyPath)
	}
	if cfg.Crypto.PublicKeyPath != "" && !filepath.IsAbs(cfg.Crypto.PublicKeyPath) {
		cfg.Crypto.PublicKeyPath = filepath.Join(dir, cfg.Crypto.PublicKeyPath)
	}
	if cfg.Crypto.DataDir != "" && !filepath.IsAbs(cfg.Crypto.DataDir) {
		cfg.Crypto.DataDir = filepath.Join(dir, cfg.Crypto.DataDir)
	}

	normalised, err := normalizeAndValidateAdmins(cfg.Admins)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Admins = normalised
	cfg.Path = path

	return cfg, nil
}

// Save writes the config to a JSON file.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
