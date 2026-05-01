package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
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
	// Provider names the AUTH STRATEGY that minted this identity
	// ("session", "token", "gateway", "auth-disabled"). The policy
	// evaluator keys on this to decide trust tiers; do not repurpose.
	Provider string `json:"provider"`
	// AccountProvider names the account-store provider the identity
	// resolves to (local, microsoft, github, entra, gateway). Zero
	// value for strategies that don't own an account row (e.g.
	// bearer tokens — tokens are bound to accounts via TokenEntry,
	// not via the identity itself). Handlers that need to load the
	// caller's account row use this field, not Provider.
	AccountProvider string `json:"account_provider,omitempty"`
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
// The strategy list is guarded by strategiesMu so it can be mutated
// at runtime (providers admin page) without racing the middleware
// iteration. The enabled / public-prefix state is set once at boot
// and read-only afterward, so it stays lock-free.
type Provider struct {
	strategiesMu sync.RWMutex
	strategies   []Strategy

	enabled        bool
	publicExact    []string
	publicPrefixes []string
	// basicPrefixes lists URL prefixes where HTTP Basic auth is
	// accepted. Outside these prefixes a Basic header is treated as
	// "no credentials" and the 401 challenge advertises Bearer only.
	// Populated via MarkBasicPrefix — we deliberately narrow Basic's
	// surface because it exists solely to make git-over-HTTP work
	// (git-the-binary can't send Bearer). Every other client of the
	// JSON API can and should use Bearer.
	basicPrefixes []string
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
	p.strategiesMu.Lock()
	defer p.strategiesMu.Unlock()
	p.strategies = append(p.strategies, s)
}

// Replace swaps the existing strategy with the same Name() for s.
// Returns true when a replacement happened, false when no matching
// strategy existed (caller can decide to Register instead). Used by
// the /admin/auth reload path so e.g. a new gateway strategy atomically
// takes the old one's slot without disturbing the iteration order
// (which defines the try-in-sequence contract).
func (p *Provider) Replace(s Strategy) bool {
	p.strategiesMu.Lock()
	defer p.strategiesMu.Unlock()
	name := s.Name()
	for i, existing := range p.strategies {
		if existing.Name() == name {
			p.strategies[i] = s
			return true
		}
	}
	return false
}

// Remove deletes the strategy with that Name() from the list, if any.
// Returns true when something was removed. The reload path uses this
// to drop a gateway strategy when the admin flips enabled=false.
func (p *Provider) Remove(name string) bool {
	p.strategiesMu.Lock()
	defer p.strategiesMu.Unlock()
	for i, existing := range p.strategies {
		if existing.Name() == name {
			p.strategies = append(p.strategies[:i], p.strategies[i+1:]...)
			return true
		}
	}
	return false
}

// snapshotStrategies returns a copy of the current strategy list so
// the middleware can iterate without holding the lock while running
// arbitrary user Authenticate() code (some strategies touch the
// accounts store, which has its own lock — nested locks would invite
// deadlocks on the reload path).
func (p *Provider) snapshotStrategies() []Strategy {
	p.strategiesMu.RLock()
	defer p.strategiesMu.RUnlock()
	out := make([]Strategy, len(p.strategies))
	copy(out, p.strategies)
	return out
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

// MarkBasicPrefix whitelists a URL prefix for HTTP Basic auth. Outside
// any whitelisted prefix the middleware rejects Basic with a 401 that
// advertises Bearer only — so a caller who somehow ended up sending
// Basic to /api/admin/* hears "use Bearer" explicitly instead of
// silently getting token lookup + policy evaluation. Narrowing Basic's
// surface is a defence-in-depth move; the token strategy would accept
// it anyway since tokens are self-identifying.
func (p *Provider) MarkBasicPrefix(prefix string) {
	p.basicPrefixes = append(p.basicPrefixes, prefix)
}

// basicAllowedFor reports whether the path sits under any prefix
// registered via MarkBasicPrefix.
func (p *Provider) basicAllowedFor(urlPath string) bool {
	for _, pp := range p.basicPrefixes {
		if strings.HasPrefix(urlPath, pp) {
			return true
		}
	}
	return false
}

// requestUsesBasic reports whether the incoming Authorization header
// is a Basic challenge. Used by the middleware to path-scope Basic.
func requestUsesBasic(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if h == "" {
		return false
	}
	parts := strings.SplitN(h, " ", 2)
	return len(parts) == 2 && strings.EqualFold(parts[0], "Basic")
}

// wantsHTML reports whether the request looks like a browser
// navigation that should land on a page rather than receive a 401
// text body. Browsers send Accept: text/html,... on top-level GETs;
// fetch()/curl/git default to */* or application/json so they keep
// the original 401 path and the JS guard layer keeps working.
func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
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
	for _, s := range p.snapshotStrategies() {
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

		// Path-scope Basic: outside registered prefixes, refuse Basic
		// outright rather than running token lookup + policy. This
		// keeps the Basic attack surface limited to /git/* (where git
		// the binary actually needs it) instead of every bearer-gated
		// route. Bearer is always accepted.
		basicAllowedHere := p.basicAllowedFor(r.URL.Path)
		if !basicAllowedHere && requestUsesBasic(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gigot"`)
			http.Error(w, "unauthorized (use Bearer on this path)", http.StatusUnauthorized)
			return
		}

		id, err := p.Authenticate(r)
		if err != nil {
			// Browser navigations bounce to the public landing page
			// instead of rendering a bare "unauthorized" body in the
			// address bar — dead-end UX with no path back to sign-in.
			// API callers (Accept: */* or application/json) still get
			// 401 so the admin SPA's guardSession() keeps working.
			if wantsHTML(r) {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			// Challenge advertises whichever scheme the caller would be
			// able to retry with on this path. /git/* gets Basic (which
			// is what git-the-binary understands); everything else gets
			// Bearer. Keeps the "what credential should I send?" answer
			// in the response itself.
			scheme := "Bearer"
			if basicAllowedHere {
				scheme = "Basic"
			}
			w.Header().Set("WWW-Authenticate", scheme+` realm="gigot"`)
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
