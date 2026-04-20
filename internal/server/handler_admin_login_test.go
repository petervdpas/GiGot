package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

func postLogin(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAdminLogin_RejectsRegularRoleAs401(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "carol",
		Role:       accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.SetPassword("carol", "hunter2"); err != nil {
		t.Fatal(err)
	}
	rec := postLogin(t, srv, `{"username":"carol","password":"hunter2"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for password-ok/role-regular, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_AcceptsAdminRole(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:    accounts.ProviderLocal,
		Identifier:  "carol",
		Role:        accounts.RoleAdmin,
		DisplayName: "Carol",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.SetPassword("carol", "hunter2"); err != nil {
		t.Fatal(err)
	}
	rec := postLogin(t, srv, `{"username":"carol","password":"hunter2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for admin role, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"username":"carol"`, `"display_name":"Carol"`, `"role":"admin"`} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("response missing %q: %s", want, body)
		}
	}
}

func TestAdminLogin_WrongPassword(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: "carol",
		Role:       accounts.RoleAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.accounts.SetPassword("carol", "hunter2"); err != nil {
		t.Fatal(err)
	}
	rec := postLogin(t, srv, `{"username":"carol","password":"wrong"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong password, got %d", rec.Code)
	}
}

func TestAdminLogin_Returns404WhenAllowLocalFalse(t *testing.T) {
	srv := testServer(t)
	srv.cfg.Auth.AllowLocal = false
	rec := postLogin(t, srv, `{"username":"admin","password":"pw"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 when allow_local=false, got %d body=%s", rec.Code, rec.Body.String())
	}
}
