package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervdpas/GiGot/internal/accounts"
)

func TestRegister_CreatesRegularAccount(t *testing.T) {
	srv := testServer(t)
	body := `{"username":"newuser","password":"pw123456","display_name":"New User"}`
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	acc, err := srv.accounts.Get(accounts.ProviderLocal, "newuser")
	if err != nil {
		t.Fatalf("account not created: %v", err)
	}
	if acc.Role != accounts.RoleRegular {
		t.Errorf("role=%q, want regular", acc.Role)
	}
	if _, err := srv.accounts.Verify("newuser", "pw123456"); err != nil {
		t.Fatalf("password not set: %v", err)
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if !view.HasPassword {
		t.Error("response should report HasPassword=true")
	}
	if view.DisplayName != "New User" {
		t.Errorf("display_name=%q, want %q", view.DisplayName, "New User")
	}
}

// TestRegister_PersistsEmail covers the email-on-register path:
// self-service registration accepts an email, normalises it to
// lowercase+trimmed, stores it, and ships it back in the response.
func TestRegister_PersistsEmail(t *testing.T) {
	srv := testServer(t)
	body := `{"username":"newuser","password":"pw123456","email":"  Peter@Example.COM  "}`
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var view AccountView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Email != "peter@example.com" {
		t.Errorf("response Email = %q, want lowercased+trimmed", view.Email)
	}
	stored, _ := srv.accounts.Get(accounts.ProviderLocal, "newuser")
	if stored.Email != "peter@example.com" {
		t.Errorf("stored Email = %q, want lowercased+trimmed", stored.Email)
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	srv := testServer(t)
	if _, err := srv.accounts.Put(accounts.Account{
		Provider: accounts.ProviderLocal, Identifier: "taken", Role: accounts.RoleRegular,
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"username":"taken","password":"pw"}`
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRegister_RequiresPassword(t *testing.T) {
	srv := testServer(t)
	body := `{"username":"alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestRegister_404WhenLocalDisabled(t *testing.T) {
	srv := testServer(t)
	srv.cfg.Auth.AllowLocal = false

	body := `{"username":"alice","password":"pw"}`
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 when allow_local=false, got %d", rec.Code)
	}
}
