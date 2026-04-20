package oauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/petervdpas/GiGot/internal/config"
)

// SecretResolver looks up a credential secret by its name in the
// credential vault. The OAuth layer doesn't import
// internal/credentials directly so tests can stub this with a plain
// map — and so a future "secret manager" shift (KMS, SOPS, etc.)
// doesn't ripple through every provider.
type SecretResolver func(name string) (string, error)

// Registry is the set of live, discovery-completed providers.
// Callers look up a provider by its URL name ("github", "entra",
// "microsoft") on each callback. Immutable after Build — adding or
// removing a provider requires a restart, same as every other
// config field.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// Providers returns the live provider list in a stable order
// (github, entra, microsoft). Used by the login page to render
// buttons in the same order every boot.
func (r *Registry) Providers() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	order := []string{"github", "entra", "microsoft"}
	out := make([]Provider, 0, len(r.providers))
	for _, name := range order {
		if p, ok := r.providers[name]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Get returns the provider with that name, or nil if it isn't
// enabled in this deployment.
func (r *Registry) Get(name string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[name]
}

// Replace atomically sets the provider under name. Passing a nil
// provider is equivalent to Remove(name). Used by the /admin/auth
// hot-reload path so a single Registry pointer is shared across
// every in-flight callback — no torn-pointer windows when the
// operator flips a provider on or off from the UI.
func (r *Registry) Replace(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		delete(r.providers, name)
		return
	}
	r.providers[name] = p
}

// Remove drops the provider under name. Returns true when something
// was removed (caller can log, callers currently don't need the
// bool).
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[name]; !ok {
		return false
	}
	delete(r.providers, name)
	return true
}

// Build constructs a Registry from the OAuth config block. Disabled
// providers are silently skipped; enabled providers with bad config
// or unreachable discovery endpoints return an error so the server
// fails fast at boot.
func Build(ctx context.Context, cfg config.OAuthConfig, resolve SecretResolver) (*Registry, error) {
	r := &Registry{providers: make(map[string]Provider)}

	if cfg.GitHub.Enabled {
		p, err := buildGitHub(cfg.GitHub, resolve)
		if err != nil {
			return nil, err
		}
		r.providers["github"] = p
	}
	if cfg.Entra.Enabled {
		p, err := buildEntra(ctx, cfg.Entra, resolve)
		if err != nil {
			return nil, err
		}
		r.providers["entra"] = p
	}
	if cfg.Microsoft.Enabled {
		p, err := buildMicrosoft(ctx, cfg.Microsoft, resolve)
		if err != nil {
			return nil, err
		}
		r.providers["microsoft"] = p
	}
	return r, nil
}

func buildGitHub(cfg config.OAuthProviderConfig, resolve SecretResolver) (Provider, error) {
	if err := requireClientCreds("github", cfg); err != nil {
		return nil, err
	}
	secret, err := resolve(cfg.ClientSecretRef)
	if err != nil {
		return nil, fmt.Errorf("oauth: github: resolve client_secret_ref %q: %w", cfg.ClientSecretRef, err)
	}
	display := cfg.DisplayName
	if display == "" {
		display = "GitHub"
	}
	return NewGitHubProvider(cfg.ClientID, secret, display, cfg.AllowRegister), nil
}

func buildEntra(ctx context.Context, cfg config.OAuthProviderConfig, resolve SecretResolver) (Provider, error) {
	if err := requireClientCreds("entra", cfg); err != nil {
		return nil, err
	}
	tenant := strings.TrimSpace(cfg.TenantID)
	if tenant == "" {
		return nil, errors.New("oauth: entra: tenant_id is required")
	}
	secret, err := resolve(cfg.ClientSecretRef)
	if err != nil {
		return nil, fmt.Errorf("oauth: entra: resolve client_secret_ref %q: %w", cfg.ClientSecretRef, err)
	}
	display := cfg.DisplayName
	if display == "" {
		display = "Microsoft (work or school)"
	}
	issuer := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenant)
	return NewOIDCProvider(ctx, "entra", issuer, cfg.ClientID, secret, display, "oid", cfg.AllowRegister)
}

func buildMicrosoft(ctx context.Context, cfg config.OAuthProviderConfig, resolve SecretResolver) (Provider, error) {
	if err := requireClientCreds("microsoft", cfg); err != nil {
		return nil, err
	}
	secret, err := resolve(cfg.ClientSecretRef)
	if err != nil {
		return nil, fmt.Errorf("oauth: microsoft: resolve client_secret_ref %q: %w", cfg.ClientSecretRef, err)
	}
	display := cfg.DisplayName
	if display == "" {
		display = "Microsoft (personal)"
	}
	return NewOIDCProvider(ctx, "microsoft",
		"https://login.microsoftonline.com/consumers/v2.0",
		cfg.ClientID, secret, display, "sub", cfg.AllowRegister)
}

func requireClientCreds(name string, cfg config.OAuthProviderConfig) error {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return fmt.Errorf("oauth: %s: client_id is required when enabled=true", name)
	}
	if strings.TrimSpace(cfg.ClientSecretRef) == "" {
		return fmt.Errorf("oauth: %s: client_secret_ref is required when enabled=true", name)
	}
	return nil
}
