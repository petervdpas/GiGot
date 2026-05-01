package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	oauth2github "golang.org/x/oauth2/github"
)

// Provider is the abstraction every concrete IdP implements. The
// redirect-flow handler calls AuthURL to start, then ExchangeAndClaim
// on the callback — everything between those two points is the IdP's
// problem. Name is the URL segment used in
// /admin/login/<name>/callback, DisplayName is the button label.
type Provider interface {
	Name() string
	DisplayName() string
	AllowsRegister() bool
	AuthURL(redirectURI, state, nonce, codeChallenge string) string
	ExchangeAndClaim(ctx context.Context, code, codeVerifier, redirectURI, nonce string) (Claim, error)
}

// Claim is the normalized view of a successful IdP login — the
// identifier GiGot uses to key the Account row plus a best-effort
// display name and email. Provider-specific quirks (GitHub's email
// requiring a separate API call, Entra's `oid` being a GUID) are
// hidden behind this shape.
//
// Email is populated independently of Identifier so accounts have a
// stable user-facing handle even when the identifier is something
// machine-shaped (entra `oid`, GitHub `login` historically). The
// callback handler writes it to Account.Email and refreshes it on
// every successful login.
type Claim struct {
	Identifier  string
	DisplayName string
	Email       string
}

// PKCEChallenge returns the S256 code_challenge for a given verifier.
// Exported so the handler can compute the challenge once and feed it
// straight into the AuthURL call without round-tripping through a
// provider-specific method.
func PKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ---------------------------------------------------------------- GitHub

// GitHubProvider is the GitHub-OAuth flavour. GitHub doesn't emit an
// ID token, so after the standard OAuth exchange we hit /user with
// the access token and read the `login` field. The login is the
// Account identifier — case-insensitive on GitHub's side, lowercased
// here to match the accounts store.
type GitHubProvider struct {
	cfg           oauth2.Config
	displayName   string
	allowRegister bool
	userAPIURL    string // override for tests; "" → api.github.com
}

// NewGitHubProvider constructs the GitHub provider. clientSecret is
// the resolved secret (looked up from the vault by the caller). The
// `user:email` scope is required because the account identifier is
// the user's primary verified email (read from /user/emails) — the
// public `email` field on /user is null for users who haven't opted
// in to a public email, and we'd rather index every user than only
// those who set one.
func NewGitHubProvider(clientID, clientSecret, displayName string, allowRegister bool) *GitHubProvider {
	return &GitHubProvider{
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     oauth2github.Endpoint,
			Scopes:       []string{"read:user", "user:email"},
		},
		displayName:   displayName,
		allowRegister: allowRegister,
	}
}

func (p *GitHubProvider) Name() string         { return "github" }
func (p *GitHubProvider) DisplayName() string  { return p.displayName }
func (p *GitHubProvider) AllowsRegister() bool { return p.allowRegister }

func (p *GitHubProvider) AuthURL(redirectURI, state, _, _ string) string {
	// GitHub OAuth doesn't use nonce or PKCE (PKCE was rejected by
	// GitHub at the time of writing — watch for future support). State
	// alone is the CSRF barrier. We still keep the nonce / challenge
	// signature on the interface so the handler can stay uniform.
	cfg := p.cfg
	cfg.RedirectURL = redirectURI
	return cfg.AuthCodeURL(state)
}

func (p *GitHubProvider) ExchangeAndClaim(ctx context.Context, code, _, redirectURI, _ string) (Claim, error) {
	cfg := p.cfg
	cfg.RedirectURL = redirectURI
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return Claim{}, fmt.Errorf("github: exchange: %w", err)
	}
	userURL := p.userAPIURL
	if userURL == "" {
		userURL = "https://api.github.com/user"
	}
	user, err := p.fetchUser(ctx, userURL, tok.AccessToken)
	if err != nil {
		return Claim{}, err
	}
	// /user/emails is a peer endpoint; suffixing the configured user
	// URL keeps the test fixture (single httptest server) able to
	// route both calls without growing a second knob.
	email, err := p.fetchPrimaryEmail(ctx, userURL+"/emails", tok.AccessToken)
	if err != nil {
		return Claim{}, err
	}
	// Identifier IS the email, lowercased. Email field on Claim
	// duplicates it for callers that key on Identifier but still
	// want a semantic email handle to write to Account.Email.
	loweredEmail := strings.ToLower(email)
	return Claim{
		Identifier:  loweredEmail,
		Email:       loweredEmail,
		DisplayName: strings.TrimSpace(user.Name),
	}, nil
}

// gitHubUser is the subset of /user we read. Login is no longer the
// account identifier (email took over) but we still pluck Name for
// the human-readable display label.
type gitHubUser struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

func (p *GitHubProvider) fetchUser(ctx context.Context, url, token string) (gitHubUser, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gitHubUser{}, fmt.Errorf("github: /user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return gitHubUser{}, fmt.Errorf("github: /user: %s: %s", resp.Status, string(body))
	}
	var u gitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return gitHubUser{}, fmt.Errorf("github: decode /user: %w", err)
	}
	return u, nil
}

// fetchPrimaryEmail returns the user's primary verified email from
// /user/emails. Errors out if no verified primary exists — that
// state means the account has no usable identity from our model's
// perspective and we'd rather fail clearly than fall back to login
// (which would silently re-introduce the per-app duplicate-account
// problem the email switch was meant to fix).
func (p *GitHubProvider) fetchPrimaryEmail(ctx context.Context, url, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: /user/emails: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github: /user/emails: %s: %s", resp.Status, string(body))
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", fmt.Errorf("github: decode /user/emails: %w", err)
	}
	for _, e := range emails {
		if e.Primary && e.Verified && e.Email != "" {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("github: no primary verified email on account")
}

// ---------------------------------------------------------------- OIDC

// OIDCProvider is the generic OIDC redirect-flow flavour used for
// both entra (tenant-scoped work/school) and microsoft (consumer
// MSA). The only differences are the discovery URL and the claim
// that becomes the Account identifier; both are configured at
// construction.
type OIDCProvider struct {
	name            string
	displayName     string
	allowRegister   bool
	identifierClaim string // "oid" for entra, "sub" for microsoft
	cfg             oauth2.Config
	verifier        *oidc.IDTokenVerifier
}

// NewOIDCProvider fetches the issuer's OIDC discovery doc, builds
// the verifier, and stores the OAuth2 config. Returns an error if
// discovery fails so a broken provider fails fast at boot, not on
// the first click. The context is only used for discovery — pass a
// short-lived one.
func NewOIDCProvider(ctx context.Context, name, issuerURL, clientID, clientSecret, displayName, identifierClaim string, allowRegister bool) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("oauth: %s: discover %s: %w", name, issuerURL, err)
	}
	return &OIDCProvider{
		name:            name,
		displayName:     displayName,
		allowRegister:   allowRegister,
		identifierClaim: identifierClaim,
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

func (p *OIDCProvider) Name() string         { return p.name }
func (p *OIDCProvider) DisplayName() string  { return p.displayName }
func (p *OIDCProvider) AllowsRegister() bool { return p.allowRegister }

func (p *OIDCProvider) AuthURL(redirectURI, state, nonce, codeChallenge string) string {
	cfg := p.cfg
	cfg.RedirectURL = redirectURI
	return cfg.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func (p *OIDCProvider) ExchangeAndClaim(ctx context.Context, code, codeVerifier, redirectURI, nonce string) (Claim, error) {
	cfg := p.cfg
	cfg.RedirectURL = redirectURI
	tok, err := cfg.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return Claim{}, fmt.Errorf("oauth: %s: exchange: %w", p.name, err)
	}
	rawIDToken, _ := tok.Extra("id_token").(string)
	if rawIDToken == "" {
		return Claim{}, fmt.Errorf("oauth: %s: no id_token in response", p.name)
	}
	idTok, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Claim{}, fmt.Errorf("oauth: %s: verify id_token: %w", p.name, err)
	}
	if idTok.Nonce != nonce {
		return Claim{}, fmt.Errorf("oauth: %s: nonce mismatch", p.name)
	}
	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		return Claim{}, fmt.Errorf("oauth: %s: decode claims: %w", p.name, err)
	}
	idClaim, ok := claims[p.identifierClaim].(string)
	if !ok || idClaim == "" {
		return Claim{}, fmt.Errorf("oauth: %s: claim %q missing or not a string", p.name, p.identifierClaim)
	}
	displayName, _ := claims["name"].(string)
	// Email is read independently — populated even when it isn't the
	// identifier (entra keys on `oid`, but we still want Account.Email
	// for Formidable's "signed in as" surface). Missing is OK; we
	// just leave Claim.Email empty.
	rawEmail, _ := claims["email"].(string)
	return Claim{
		Identifier:  strings.ToLower(idClaim),
		Email:       strings.ToLower(strings.TrimSpace(rawEmail)),
		DisplayName: strings.TrimSpace(displayName),
	}, nil
}
