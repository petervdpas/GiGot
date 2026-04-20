package server

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/auth/gateway"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/credentials"
)

// gatewayStrategy is the server-side auth.Strategy that bridges the
// header-only gateway.Verifier to GiGot's accounts store. Registered
// with the auth.Provider so the middleware layer attaches an
// identity to the request context, and also called directly by
// requireAdminSession so gateway admins reach the admin UI without
// needing a session cookie. See docs/design/accounts.md §9.
type gatewayStrategy struct {
	verifier      *gateway.Verifier
	accounts      *accounts.Store
	allowRegister bool
}

func (g *gatewayStrategy) Name() string { return "gateway" }

// Authenticate returns an Identity for a request carrying a valid
// signed-header triple, or ErrNoCredentials when no gateway headers
// are present so the next strategy in the chain (e.g. bearer token)
// still gets a chance. A malformed-but-present header triple is a
// hard ErrInvalidToken — a forged claim must never silently fall
// through to the next strategy.
func (g *gatewayStrategy) Authenticate(r *http.Request) (*auth.Identity, error) {
	claim, err := g.verifier.Verify(r)
	if err != nil {
		if errors.Is(err, gateway.ErrHeaderMissing) {
			return nil, auth.ErrNoCredentials
		}
		// Tampered signature, stale timestamp, malformed hex —
		// always log so a misbehaving proxy is discoverable without
		// giving the attacker anything richer than 401.
		log.Printf("server: gateway: reject request from %s: %v", r.RemoteAddr, err)
		return nil, auth.ErrInvalidToken
	}

	acc, err := g.accounts.Get(accounts.ProviderGateway, claim.Identifier)
	if errors.Is(err, accounts.ErrNotFound) {
		if !g.allowRegister {
			log.Printf("server: gateway: unknown user %q and allow_register=false", claim.Identifier)
			return nil, auth.ErrInvalidToken
		}
		acc, err = g.accounts.Put(accounts.Account{
			Provider:   accounts.ProviderGateway,
			Identifier: claim.Identifier,
			Role:       accounts.RoleRegular,
		})
		if err != nil {
			return nil, err
		}
		log.Printf("server: gateway: auto-registered %q as regular", claim.Identifier)
	} else if err != nil {
		return nil, err
	}

	return &auth.Identity{
		ID:       acc.Identifier,
		Username: acc.Identifier,
		Provider: "gateway",
	}, nil
}

// buildGatewayStrategy resolves the Phase-4 config block into a
// ready-to-register strategy. Returns (nil, nil) when disabled;
// returns a non-nil error only for "operator asked for this but
// misconfigured it" cases (no secret, missing header names, bad skew),
// so the server fails fast at boot rather than shipping with a
// silently-dead gateway path.
func buildGatewayStrategy(cfg config.GatewayConfig, creds *credentials.Store, accountStore *accounts.Store) (*gatewayStrategy, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.SecretRef == "" {
		return nil, errors.New("auth.gateway: secret_ref is required when enabled=true")
	}
	cred, err := creds.Get(cfg.SecretRef)
	if err != nil {
		return nil, err
	}
	skew := time.Duration(cfg.MaxSkewSeconds) * time.Second
	if cfg.MaxSkewSeconds <= 0 {
		skew = 5 * time.Minute
	}
	v, err := gateway.NewVerifier(gateway.Options{
		Secret:          []byte(cred.Secret),
		UserHeader:      cfg.UserHeader,
		SigHeader:       cfg.SigHeader,
		TimestampHeader: cfg.TimestampHeader,
		MaxSkew:         skew,
	})
	if err != nil {
		return nil, err
	}
	return &gatewayStrategy{
		verifier:      v,
		accounts:      accountStore,
		allowRegister: cfg.AllowRegister,
	}, nil
}
