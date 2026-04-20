package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth/gateway"
)

const gwTestSecret = "gateway-test-secret"

// installGatewayStrategy surgically replaces srv.gatewayStrategy with a
// fresh one wired against an in-memory secret. Skips the vault lookup
// path since we're only testing handler / middleware behaviour, not
// secret resolution (that's covered by the gateway package tests +
// buildGatewayStrategy unit tests below).
func installGatewayStrategy(t *testing.T, srv *Server, allowRegister bool) {
	t.Helper()
	v, err := gateway.NewVerifier(gateway.Options{
		Secret:          []byte(gwTestSecret),
		UserHeader:      "X-GiGot-Gateway-User",
		SigHeader:       "X-GiGot-Gateway-Sig",
		TimestampHeader: "X-GiGot-Gateway-Ts",
		MaxSkew:         5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	srv.gatewayStrategy = &gatewayStrategy{
		verifier:      v,
		accounts:      srv.accounts,
		allowRegister: allowRegister,
	}
	srv.auth.Register(srv.gatewayStrategy)
	srv.auth.SetEnabled(true)
}

func signGateway(t *testing.T, req *http.Request, user string) {
	t.Helper()
	sig, ts := gateway.Sign([]byte(gwTestSecret), user, time.Now())
	req.Header.Set("X-GiGot-Gateway-User", user)
	req.Header.Set("X-GiGot-Gateway-Sig", sig)
	req.Header.Set("X-GiGot-Gateway-Ts", ts)
}

// Gateway admin reaches admin UI without needing /admin/login.
func TestGateway_AdminReachesAdminAPIWithoutSession(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, false)

	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderGateway,
		Identifier: "boss@corp",
		Role:       accounts.RoleAdmin,
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	signGateway(t, req, "boss@corp")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Accounts []AccountView `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(resp.Accounts) == 0 {
		t.Fatal("want at least one account in response")
	}
}

// A regular gateway account must NOT reach the admin API (401).
func TestGateway_RegularRejectedFromAdminAPI(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, false)

	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderGateway,
		Identifier: "peer@corp",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatalf("seed regular: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	signGateway(t, req, "peer@corp")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// Unknown identifier with allow_register=false is rejected outright
// — even at the middleware layer (hard error, not a fallthrough).
func TestGateway_UnknownUserBlockedWhenRegisterDisabled(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, false)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	signGateway(t, req, "ghost@corp")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if srv.accounts.Has(accounts.ProviderGateway, "ghost@corp") {
		t.Fatal("unknown user must NOT be auto-registered when allow_register=false")
	}
}

// Unknown identifier with allow_register=true is auto-registered as
// role=regular. (Regulars can't reach admin API, so the request still
// 401s — the account creation is the load-bearing assertion.)
func TestGateway_UnknownUserAutoRegisteredAsRegular(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, true)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	signGateway(t, req, "newbie@corp")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	// Regular never sees admin API → 401 still, but account is created.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 (not admin), got %d", rec.Code)
	}
	acc, err := srv.accounts.Get(accounts.ProviderGateway, "newbie@corp")
	if err != nil {
		t.Fatalf("auto-register missed: %v", err)
	}
	if acc.Role != accounts.RoleRegular {
		t.Fatalf("role=%q, want regular", acc.Role)
	}
}

// A request with NO gateway headers must fall through to the normal
// auth path (here, a 401 from requireAdminSession since no cookie).
// Critical: a request without gateway headers is not "gateway-auth'd"
// — it's "not using the gateway," and must not short-circuit.
func TestGateway_MissingHeadersFallThrough(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, true)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// Tampered signature must fail hard — not fall through, not be allowed
// to guess the next strategy (bearer) as a workaround.
func TestGateway_TamperedSignatureIsHardReject(t *testing.T) {
	srv := testServer(t)
	installGatewayStrategy(t, srv, true)

	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderGateway,
		Identifier: "boss@corp",
		Role:       accounts.RoleAdmin,
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	signGateway(t, req, "boss@corp")
	// Swap the user header so the HMAC no longer matches.
	req.Header.Set("X-GiGot-Gateway-User", "mallory@corp")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on tampered sig, got %d", rec.Code)
	}
}
