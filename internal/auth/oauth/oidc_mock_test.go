package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockIdP is a tiny OIDC provider that speaks just enough of the
// spec for the go-oidc verifier to accept its ID tokens. Not a
// library — it's wired inside oauth package tests so it can stay as
// tight as possible (no TLS, no userinfo endpoint, no refresh tokens).
type mockIdP struct {
	server   *httptest.Server
	priv     *rsa.PrivateKey
	keyID    string
	clientID string
	// nextCode and nextClaims let each test seed what the next
	// authorize→token exchange should return.
	nextCode   string
	nextClaims map[string]any
}

func newMockIdP(t *testing.T, clientID string) *mockIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{priv: priv, keyID: "mock-key-1", clientID: clientID}

	mux := http.NewServeMux()
	// Discovery. go-oidc hits /.well-known/openid-configuration.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer := m.server.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"jwks_uri":                              issuer + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []any{map[string]any{
				"kty": "RSA",
				"kid": m.keyID,
				"use": "sig",
				"alg": "RS256",
				"n":   n,
				"e":   e,
			}},
		})
	})
	// Token endpoint: accept the exchange, produce a signed ID token
	// with the test-seeded claims. We don't validate the code — the
	// real IdP would, but that's not the code path under test here.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		claims := jwt.Claims{
			Issuer:   m.server.URL,
			Subject:  stringClaim(m.nextClaims, "sub"),
			Audience: jwt.Audience{m.clientID},
			Expiry:   jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.RS256, Key: priv},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID),
		)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		idToken, err := jwt.Signed(signer).Claims(claims).Claims(m.nextClaims).Serialize()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func stringClaim(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// TestOIDCProvider_HappyPath exercises the full OIDC round trip
// against mockIdP: build a provider via discovery, extract the
// matching claim from the ID token, confirm nonce enforcement.
func TestOIDCProvider_HappyPath(t *testing.T) {
	idp := newMockIdP(t, "client-xyz")
	idp.nextClaims = map[string]any{
		"sub":  "11111111-2222-3333-4444-555555555555",
		"oid":  "11111111-2222-3333-4444-555555555555",
		"name": "Peter van de Pas",
		// go-oidc verifier checks nonce by reading ID token's `nonce`
		// claim; fill a fixed value and pass the same through in the
		// test below.
		"nonce": "test-nonce",
	}

	ctx := context.Background()
	p, err := NewOIDCProvider(ctx, "entra", idp.server.URL, "client-xyz", "client-secret", "Entra test", "oid", true)
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}

	claim, err := p.ExchangeAndClaim(ctx, "code-abc", "verifier-xyz", "http://localhost/callback", "test-nonce")
	if err != nil {
		t.Fatalf("ExchangeAndClaim: %v", err)
	}
	wantID := "11111111-2222-3333-4444-555555555555"
	if claim.Identifier != wantID {
		t.Fatalf("Identifier = %q, want %q", claim.Identifier, wantID)
	}
	if claim.DisplayName != "Peter van de Pas" {
		t.Fatalf("DisplayName = %q", claim.DisplayName)
	}
}

func TestOIDCProvider_NonceMismatchIsRejected(t *testing.T) {
	idp := newMockIdP(t, "client-xyz")
	idp.nextClaims = map[string]any{
		"sub":   "user-1",
		"oid":   "user-1",
		"nonce": "server-issued-nonce",
	}
	ctx := context.Background()
	p, err := NewOIDCProvider(ctx, "entra", idp.server.URL, "client-xyz", "client-secret", "Entra test", "oid", true)
	if err != nil {
		t.Fatal(err)
	}

	// Pass a *different* nonce than what the IdP signed into the
	// token — ExchangeAndClaim must reject.
	_, err = p.ExchangeAndClaim(ctx, "code", "verifier", "http://localhost/callback", "attacker-nonce")
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("want nonce mismatch error, got %v", err)
	}
}

func TestOIDCProvider_MissingIdentifierClaimIsRejected(t *testing.T) {
	idp := newMockIdP(t, "client-xyz")
	idp.nextClaims = map[string]any{
		"sub":   "present",
		"nonce": "n",
		// no "oid"
	}
	ctx := context.Background()
	p, err := NewOIDCProvider(ctx, "entra", idp.server.URL, "client-xyz", "s", "Entra", "oid", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ExchangeAndClaim(ctx, "c", "v", "http://localhost/callback", "n")
	if err == nil || !strings.Contains(err.Error(), "oid") {
		t.Fatalf("want claim-missing error, got %v", err)
	}
}

// TestGitHubProvider_ExchangeAndClaim uses a stub httptest server
// for both GitHub's token endpoint AND api.github.com/user, wired
// through the provider's userAPIURL knob. Confirms the
// "access-token-then-API-call" shape that's unique to GitHub.
func TestGitHubProvider_ExchangeAndClaim(t *testing.T) {
	var tokenSrv, apiSrv *httptest.Server
	tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		// GitHub returns form-encoded by default; the oauth2 lib
		// accepts either — we go with JSON here for clarity.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "gho_test",
			"token_type":   "bearer",
			"scope":        "read:user",
		})
	}))
	t.Cleanup(tokenSrv.Close)

	apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_test" {
			http.Error(w, "bad auth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"login": "Peter-VDPas", // mixed case → must lowercase
			"name":  "Peter VDP",
		})
	}))
	t.Cleanup(apiSrv.Close)

	p := NewGitHubProvider("client-id", "client-secret", "GitHub", true)
	p.cfg.Endpoint.TokenURL = tokenSrv.URL + "/login/oauth/access_token"
	p.cfg.Endpoint.AuthURL = tokenSrv.URL + "/login/oauth/authorize"
	p.userAPIURL = apiSrv.URL

	claim, err := p.ExchangeAndClaim(context.Background(), "code", "", "http://localhost/callback", "")
	if err != nil {
		t.Fatalf("ExchangeAndClaim: %v", err)
	}
	if claim.Identifier != "peter-vdpas" {
		t.Fatalf("Identifier = %q, want lowercased login", claim.Identifier)
	}
	if claim.DisplayName != "Peter VDP" {
		t.Fatalf("DisplayName = %q", claim.DisplayName)
	}

	// AuthURL should include client_id + state and not blow up. We
	// can't assert much more since AuthCodeURL encodes random query
	// orderings.
	u, err := url.Parse(p.AuthURL("http://localhost/callback", "state-xyz", "", ""))
	if err != nil {
		t.Fatalf("bad AuthURL: %v", err)
	}
	if got := u.Query().Get("state"); got != "state-xyz" {
		t.Fatalf("state param = %q", got)
	}
	if got := u.Query().Get("client_id"); got != "client-id" {
		t.Fatalf("client_id param = %q", got)
	}
}

// TestPKCEChallengeFormat confirms the S256 challenge is the
// base64url(sha256(verifier)) form that Entra and Microsoft expect.
// A byte-length check is the cheapest way to catch a future encoder
// swap that would silently pass a raw challenge.
func TestPKCEChallengeFormat(t *testing.T) {
	c := PKCEChallenge("0123456789abcdef")
	want := 43 // base64url(32 bytes) with padding stripped
	if len(c) != want {
		t.Fatalf("challenge length = %d, want %d (%q)", len(c), want, c)
	}
}

// avoid unused-import errors when all imports are used only inside
// conditionals.
var _ = fmt.Sprint
