package auth

import "net/http"

// Provider handles authentication and authorization.
type Provider struct {
	// TODO: token store, user management
}

// NewProvider creates a new auth Provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Middleware wraps an http.Handler with authentication checks.
func (p *Provider) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: validate token/credentials
		next.ServeHTTP(w, r)
	})
}
