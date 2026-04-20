package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueToken(t *testing.T) {
	srv := testServer(t)
	payload := `{"username":"alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	var body TokenResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Username != "alice" {
		t.Errorf("expected username alice, got %s", body.Username)
	}
	if body.Token == "" {
		t.Error("expected non-empty token")
	}
}

func TestIssueTokenEmptyUsername(t *testing.T) {
	srv := testServer(t)
	payload := `{"username":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIssueTokenInvalidBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", bytes.NewBufferString("nope"))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRevokeToken(t *testing.T) {
	srv := testServer(t)

	// Issue a token first.
	token, _ := srv.tokenStrategy.Issue("bob", nil, nil)

	payload := `{"token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body MessageResponse
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Message != "token revoked" {
		t.Errorf("expected 'token revoked', got %s", body.Message)
	}
}

func TestRevokeTokenNotFound(t *testing.T) {
	srv := testServer(t)
	payload := `{"token":"nonexistent"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRevokeTokenEmptyToken(t *testing.T) {
	srv := testServer(t)
	payload := `{"token":""}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/token", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTokenMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
