package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// handleToken godoc
// @Summary      Issue or revoke API tokens
// @Description  POST issues a new token, DELETE revokes one
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      TokenRequest       false  "Token request (for POST)"
// @Param        body  body      RevokeTokenRequest false  "Revoke request (for DELETE)"
// @Success      201   {object}  TokenResponse
// @Success      200   {object}  MessageResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /auth/token [post]
// @Router       /auth/token [delete]
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.issueToken(w, r)
	case http.MethodDelete:
		s.revokeToken(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	repos := normalizeRepos(req.Repos)
	if err := s.validateRepos(repos); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	abilities := normalizeAbilities(req.Abilities)
	if err := validateAbilities(abilities); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.ensureAccountForToken(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	token, err := s.tokenStrategy.Issue(req.Username, repos, abilities)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, TokenResponse{
		Token:     token,
		Username:  req.Username,
		Repos:     repos,
		Abilities: abilities,
	})
}

// ensureAccountForToken enforces the subscription-to-account binding
// for /api/auth/token and /api/admin/tokens. The "bare username"
// shorthand resolves to (provider=local, identifier=username) — the
// only form Phase 1 supports; see docs/design/accounts.md §6.
//
// Phase 1 is permissive: if no account exists, we auto-create one
// with role=regular and log it, instead of rejecting outright. The
// purpose of the binding is that every token has a real row to bind
// to, not to gate legacy flows on prior registration — Phase 2's
// registration flow will tighten this to "reject if missing" as a
// deliberate later step. Integration tests, the Postman collection,
// and the demo flow all continue to work against arbitrary usernames
// without a prior-account step.
func (s *Server) ensureAccountForToken(username string) error {
	if s.accounts.Has(accounts.ProviderLocal, username) {
		return nil
	}
	if _, err := s.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: username,
		Role:       accounts.RoleRegular,
	}); err != nil {
		return err
	}
	log.Printf("server: auto-created regular account %q for token issuance", username)
	return nil
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	var req RevokeTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	if !s.tokenStrategy.Revoke(req.Token) {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{Message: "token revoked"})
}
