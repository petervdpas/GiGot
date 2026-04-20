package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

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

// parseTokenUsername resolves the TokenRequest.Username string into
// (provider, identifier). Accepts two shapes:
//
//   - scoped   "provider:identifier"   — e.g. "github:petervdpas",
//     "entra:<oid>", "local:alice". Introduced in Phase 3 so OAuth
//     accounts can hold subscription keys; see accounts.md §6.
//   - bare     "identifier"            — resolves to (local,
//     identifier), back-compat for callers (integration tests,
//     Postman collection, CLI demos) that were written before the
//     accounts model.
//
// Empty string is caller error and surfaces as 400.
func parseTokenUsername(s string) (provider, identifier string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("username is required")
	}
	// If the part before the first ":" names a known provider, treat
	// it as scoped. Otherwise the colon is just a legal character in
	// the identifier (rare but OIDC subs can contain anything) and
	// we fall back to the bare-string → local shorthand.
	if i := strings.IndexByte(s, ':'); i > 0 {
		head := strings.ToLower(s[:i])
		if slices.Contains(accounts.KnownProviders, head) {
			id := strings.ToLower(strings.TrimSpace(s[i+1:]))
			if id == "" {
				return "", "", fmt.Errorf("identifier is required after %q:", head)
			}
			return head, id, nil
		}
	}
	return accounts.ProviderLocal, strings.ToLower(s), nil
}

// ensureAccountForToken enforces the subscription-to-account binding
// for /api/auth/token and /api/admin/tokens. The username is parsed
// via parseTokenUsername, and the resolved (provider, identifier)
// must already exist in the accounts store — no permissive
// auto-create (Phase 2 retired that). See accounts.md §6.
func (s *Server) ensureAccountForToken(username string) error {
	provider, identifier, err := parseTokenUsername(username)
	if err != nil {
		return err
	}
	if s.accounts.Has(provider, identifier) {
		return nil
	}
	return fmt.Errorf("no %s account for %q — register via /register or create one via POST /api/admin/accounts before issuing a token", provider, identifier)
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
