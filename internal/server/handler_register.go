package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// handleRegister godoc
// @Summary      Self-service registration for a local regular account
// @Description  Creates a new local-provider account with role=regular
// @Description  and sets the bcrypt password. Returns 404 when
// @Description  auth.allow_local is false (local path disabled), 409
// @Description  when the username is already taken. Public: no session
// @Description  required. See docs/design/accounts.md §7.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      RegisterRequest   true  "Registration body"
// @Success      201   {object}  AccountView
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /register [post]
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.cfg.Auth.AllowLocal {
		writeError(w, http.StatusNotFound, "local registration disabled")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	if s.accounts.Has(accounts.ProviderLocal, username) {
		// Opaque 409: we don't enumerate which usernames exist, but a
		// deliberate register action deserves a clearer signal than
		// `invalid credentials`. The tradeoff is intentional — registration
		// is a public page anyway.
		writeError(w, http.StatusConflict, "username already taken")
		return
	}

	stored, err := s.accounts.Put(accounts.Account{
		Provider:    accounts.ProviderLocal,
		Identifier:  username,
		Role:        accounts.RoleRegular,
		DisplayName: strings.TrimSpace(req.DisplayName),
	})
	if err != nil {
		if errors.Is(err, accounts.ErrBadProvider) || errors.Is(err, accounts.ErrBadRole) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.accounts.SetPassword(username, req.Password); err != nil {
		// Best-effort rollback: Put succeeded but password failed, so
		// leave no half-activated row that an attacker could try empty
		// passwords against later.
		_ = s.accounts.Remove(accounts.ProviderLocal, username)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("server: /register: created regular account %q", username)
	refreshed, err := s.accounts.Get(accounts.ProviderLocal, username)
	if err == nil {
		stored = refreshed
	}
	writeJSON(w, http.StatusCreated, accountView(*stored))
}
