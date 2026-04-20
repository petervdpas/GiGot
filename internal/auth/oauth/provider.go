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
// display name. Provider-specific quirks (GitHub's `login` being
// tied to a REST call, Entra's `oid` being a GUID) are hidden behind
// this shape.
type Claim struct {
	Identifier  string
	DisplayName string
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
// the resolved secret (looked up from the vault by the caller).
func NewGitHubProvider(clientID, clientSecret, displayName string, allowRegister bool) *GitHubProvider {
	return &GitHubProvider{
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     oauth2github.Endpoint,
			Scopes:       []string{"read:user"},
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
	url := p.userAPIURL
	if url == "" {
		url = "https://api.github.com/user"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Claim{}, fmt.Errorf("github: /user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Claim{}, fmt.Errorf("github: /user: %s: %s", resp.Status, string(body))
	}
	var u struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return Claim{}, fmt.Errorf("github: decode /user: %w", err)
	}
	if u.Login == "" {
		return Claim{}, fmt.Errorf("github: /user returned empty login")
	}
	return Claim{
		Identifier:  strings.ToLower(u.Login),
		DisplayName: strings.TrimSpace(u.Name),
	}, nil
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
	return Claim{
		Identifier:  strings.ToLower(idClaim),
		DisplayName: strings.TrimSpace(displayName),
	}, nil
}
