package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/auth/oauth"
)

// handleOAuthProviders godoc
// @Summary      List enabled OAuth providers
// @Description  Returns the providers the login page should render
// @Description  buttons for. Public — the login card needs this
// @Description  before any session exists. One entry per enabled
// @Description  provider, in a stable order.
// @Tags         auth
// @Produce      json
// @Success      200  {object}  OAuthProvidersResponse
// @Router       /admin/providers [get]
func (s *Server) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	out := OAuthProvidersResponse{Providers: []OAuthProviderView{}}
	if s.oauthProviders != nil {
		for _, p := range s.oauthProviders.Providers() {
			out.Providers = append(out.Providers, OAuthProviderView{
				Name:        p.Name(),
				DisplayName: p.DisplayName(),
				LoginURL:    "/admin/login/" + p.Name(),
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOAuthLogin is the single entry point for every provider
// start/callback. The URL shape is
// /admin/login/{provider}[/callback]; anything that doesn't match
// 404s. See docs/design/accounts.md §8.
func (s *Server) handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.oauthProviders == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/admin/login/")
	if rest == r.URL.Path || rest == "" {
		http.NotFound(w, r)
		return
	}
	name, tail, _ := strings.Cut(rest, "/")
	provider := s.oauthProviders.Get(name)
	if provider == nil {
		http.NotFound(w, r)
		return
	}
	switch tail {
	case "":
		s.oauthStart(w, r, provider)
	case "callback":
		s.oauthCallback(w, r, provider)
	default:
		http.NotFound(w, r)
	}
}

// oauthStart mints a fresh state+nonce+PKCE triple, stashes it in
// the state store, and 302s the browser off to the IdP. The caller
// never sees any of those values directly — they round-trip via the
// IdP and come back on the callback URL.
func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request, p oauth.Provider) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	nonce, err := randomOAuthToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	verifier, err := randomOAuthToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	state, err := s.oauthState.Put(oauth.StateEntry{
		Provider:     p.Name(),
		Nonce:        nonce,
		CodeVerifier: verifier,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	redirectURI := oauthRedirectURI(r, p.Name())
	url := p.AuthURL(redirectURI, state, nonce, oauth.PKCEChallenge(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// oauthCallback consumes the matching state entry, runs the
// provider-specific exchange + claim extraction, resolves the
// Account, and mints the same session cookie the local login
// handler uses. All error paths are opaque to the user — one
// "login failed" landing page so the IdP doesn't become a user
// enumeration oracle.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request, p oauth.Provider) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// IdP-reported error on the way back ("user denied consent",
	// "invalid_scope"). Log it server-side; keep the user-facing
	// message neutral.
	if e := r.URL.Query().Get("error"); e != "" {
		desc := r.URL.Query().Get("error_description")
		log.Printf("server: oauth: %s: IdP returned error=%q description=%q", p.Name(), e, desc)
		oauthLoginFailed(w, r, "sign-in was cancelled")
		return
	}
	state := r.URL.Query().Get("state")
	entry, err := s.oauthState.Take(state)
	if err != nil || entry.Provider != p.Name() {
		log.Printf("server: oauth: %s: bad state: %v", p.Name(), err)
		oauthLoginFailed(w, r, "sign-in expired — try again")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		oauthLoginFailed(w, r, "sign-in did not return a code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	claim, err := p.ExchangeAndClaim(ctx, code, entry.CodeVerifier, oauthRedirectURI(r, p.Name()), entry.Nonce)
	if err != nil {
		log.Printf("server: oauth: %s: exchange: %v", p.Name(), err)
		oauthLoginFailed(w, r, "sign-in failed")
		return
	}

	// Resolve the account. If the provider allows self-registration,
	// auto-create a regular row; otherwise reject with a landing
	// message that points at an admin.
	acc, err := s.accounts.Get(p.Name(), claim.Identifier)
	if errors.Is(err, accounts.ErrNotFound) {
		if !p.AllowsRegister() {
			log.Printf("server: oauth: %s: unknown %s:%s, allow_register=false", p.Name(), p.Name(), claim.Identifier)
			oauthLoginFailed(w, r, "no account for this identity — ask an admin to register you")
			return
		}
		created, err := s.accounts.Put(accounts.Account{
			Provider:    p.Name(),
			Identifier:  claim.Identifier,
			Role:        accounts.RoleRegular,
			DisplayName: claim.DisplayName,
			Email:       claim.Email,
		})
		if err != nil {
			log.Printf("server: oauth: %s: auto-register %s: %v", p.Name(), claim.Identifier, err)
			oauthLoginFailed(w, r, "could not register your account")
			return
		}
		acc = created
		log.Printf("server: oauth: %s: auto-registered %s:%s as regular", p.Name(), p.Name(), claim.Identifier)
	} else if err != nil {
		log.Printf("server: oauth: %s: lookup %s: %v", p.Name(), claim.Identifier, err)
		oauthLoginFailed(w, r, "sign-in failed")
		return
	} else {
		// Existing account — refresh DisplayName/Email from the IdP if
		// they changed upstream so the row never goes stale. Role and
		// PasswordHash are NOT touched (preserves admin promotions and
		// any local password). An empty incoming value is treated as
		// "IdP didn't send this claim" rather than "user cleared it";
		// we keep the stored value rather than clobber.
		dirty := false
		if claim.DisplayName != "" && claim.DisplayName != acc.DisplayName {
			acc.DisplayName = claim.DisplayName
			dirty = true
		}
		if claim.Email != "" && claim.Email != acc.Email {
			acc.Email = claim.Email
			dirty = true
		}
		if dirty {
			if updated, perr := s.accounts.Put(*acc); perr == nil {
				acc = updated
			} else {
				log.Printf("server: oauth: %s: refresh %s: %v", p.Name(), claim.Identifier, perr)
			}
		}
	}

	sess, err := s.sessionStrategy.Create(acc.Provider, acc.Identifier)
	if err != nil {
		log.Printf("server: oauth: %s: create session: %v", p.Name(), err)
		oauthLoginFailed(w, r, "sign-in failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// Role-based landing: admins go to the admin console, regulars
	// land on /user (their own profile + subscription keys). The admin
	// routes still enforce role in requireAdminSession, so a regular
	// typing /admin/repositories manually just gets a 401 + bounce.
	dest := "/admin/repositories"
	if acc.Role != accounts.RoleAdmin {
		dest = "/user"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// oauthRedirectURI builds the callback URL the IdP should send the
// browser back to. Derived from the request's Host + scheme so a
// deployment behind any proxy "just works" as long as the operator
// has registered the same URL with the IdP. Deliberately not
// configurable yet — adding a config field for this is cheap, but
// we don't need it until someone hits a case the Host header can't
// cover.
func oauthRedirectURI(r *http.Request, providerName string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/admin/login/%s/callback", scheme, r.Host, providerName)
}

// oauthLoginFailed renders a minimal HTML page that says "login
// failed" + a Back-to-login link. Kept inline so we don't need a
// whole template for a one-time error page. All error branches
// funnel through here so the IdP can't be used as an oracle.
func oauthLoginFailed(w http.ResponseWriter, _ *http.Request, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Sign-in failed</title>
<link rel="stylesheet" href="/assets/admin.css"></head>
<body><div class="login-wrap card">
<h2>Sign-in failed</h2>
<p class="muted">%s</p>
<p class="login-footer"><a href="/admin">Back to sign-in</a></p>
</div></body></html>`, htmlEscape(reason))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// randomOAuthToken returns the nonce / code-verifier values the
// start handler needs. 32 raw bytes, URL-safe encoding — same
// entropy the oauth package's state generator uses.
func randomOAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
