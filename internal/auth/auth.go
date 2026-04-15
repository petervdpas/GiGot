package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Errors returned by strategies.
var (
	ErrNoCredentials    = errors.New("no credentials provided")
	ErrInvalidToken     = errors.New("invalid or expired token")
	ErrStrategyNotFound = errors.New("no matching authentication strategy")
)

type contextKey string

const (
	identityKey   contextKey = "gigot-identity"
	tokenEntryKey contextKey = "gigot-token-entry"
)

// WithTokenEntry returns a context with the given TokenEntry attached, so
// downstream consumers (primarily the policy evaluator) can read provider-
// specific attributes (currently the repo allowlist) without the generic
// Identity type needing to carry them.
func WithTokenEntry(ctx context.Context, entry *TokenEntry) context.Context {
	return context.WithValue(ctx, tokenEntryKey, entry)
}

// TokenEntryFromContext returns the TokenEntry the request was authenticated
// with, if any. Returns nil for session-authenticated requests, anonymous
// requests, and when the caller forgot to stash it.
func TokenEntryFromContext(ctx context.Context) *TokenEntry {
	e, _ := ctx.Value(tokenEntryKey).(*TokenEntry)
	return e
}

// Identity represents an authenticated user or client.
type Identity struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Provider string `json:"provider"`
}

// Strategy is the interface every authentication method implements.
type Strategy interface {
	// Name returns the strategy identifier (e.g. "token", "oidc").
	Name() string

	// Authenticate inspects the request and returns an Identity.
	// Returns ErrNoCredentials if this strategy doesn't apply to the request,
	// allowing the provider to try the next strategy.
	Authenticate(r *http.Request) (*Identity, error)
}

// Provider manages multiple strategies and acts as HTTP middleware.
type Provider struct {
	strategies      []Strategy
	enabled         bool
	publicExact     []string
	publicPrefixes  []string
}

// NewProvider creates a new auth Provider.
func NewProvider() *Provider {
	return &Provider{}
}

// SetEnabled controls whether authentication is enforced.
func (p *Provider) SetEnabled(enabled bool) {
	p.enabled = enabled
}

// Register adds a strategy to the provider. Strategies are tried in order.
func (p *Provider) Register(s Strategy) {
	p.strategies = append(p.strategies, s)
}

// MarkPublic excludes an exact path from authentication.
func (p *Provider) MarkPublic(path string) {
	p.publicExact = append(p.publicExact, path)
}

// MarkPublicPrefix excludes any path starting with the given prefix.
// prefix should be a concrete directory (e.g. "/admin/" or "/swagger/").
func (p *Provider) MarkPublicPrefix(prefix string) {
	p.publicPrefixes = append(p.publicPrefixes, prefix)
}

// isPublic reports whether the request path is marked public.
func (p *Provider) isPublic(urlPath string) bool {
	for _, pp := range p.publicExact {
		if urlPath == pp {
			return true
		}
	}
	for _, pp := range p.publicPrefixes {
		if strings.HasPrefix(urlPath, pp) {
			return true
		}
	}
	return false
}

// Authenticate tries each registered strategy in order.
// Returns the first successful Identity, or an error if all fail.
func (p *Provider) Authenticate(r *http.Request) (*Identity, error) {
	for _, s := range p.strategies {
		id, err := s.Authenticate(r)
		if err == nil {
			return id, nil
		}
		if errors.Is(err, ErrNoCredentials) {
			continue
		}
		// Hard error (e.g. invalid token) — stop immediately.
		return nil, err
	}
	return nil, ErrNoCredentials
}

// Middleware wraps an http.Handler with authentication checks. It also
// attaches an Identity to the request context on successful auth, so
// downstream policy checks can read it.
//
// When authentication is disabled globally, a sentinel "auth-disabled"
// Identity is attached instead, so policy evaluators treat the request as
// authenticated. This keeps dev mode usable without leaking Identity:nil
// through to handlers that expect to evaluate a policy.
func (p *Provider) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.enabled {
			ctx := context.WithValue(r.Context(), identityKey, authDisabledIdentity)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if p.isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		id, err := p.Authenticate(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), identityKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authDisabledIdentity is injected when Provider.enabled is false. It is a
// stable sentinel so tests can assert on it. Handlers should not special-case
// it — policy evaluators already handle authenticated callers.
var authDisabledIdentity = &Identity{
	ID:       "auth-disabled",
	Username: "auth-disabled",
	Provider: "auth-disabled",
}

// IdentityFromContext retrieves the authenticated Identity from the request context.
func IdentityFromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(identityKey).(*Identity)
	return id
}
