package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
)

// handleAdminLogin godoc
// @Summary      Admin login
// @Description  Exchanges username/password for a session cookie. Only the
// @Description  local provider is accepted on this endpoint; returns 404
// @Description  when cfg.Auth.AllowLocal is false.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      AdminLoginRequest true "Credentials"
// @Success      200   {object}  AdminLoginResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Router       /admin/login [post]
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.cfg.Auth.AllowLocal {
		writeError(w, http.StatusNotFound, "local login disabled")
		return
	}

	var req AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := s.accounts.Verify(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) || errors.Is(err, accounts.ErrInvalidPassword) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.Role != accounts.RoleAdmin {
		// Password is valid but the account isn't an admin. Return the
		// same opaque 401 as a bad password so an attacker can't use
		// /admin/login as a password oracle for regular accounts.
		log.Printf("server: /admin/login denied: account %s:%s role=%s", a.Provider, a.Identifier, a.Role)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	sess, err := s.sessionStrategy.Create(a.Identifier)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is intentionally not set here — TLS is typically terminated
		// at a gateway in our deployment target. Set via config if the server
		// itself is exposed over HTTPS.
	})
	writeJSON(w, http.StatusOK, AdminLoginResponse{
		Username:    a.Identifier,
		DisplayName: a.DisplayName,
		Role:        a.Role,
	})
}

// handleAdminLogout godoc
// @Summary      Admin logout
// @Tags         admin
// @Success      204
// @Router       /admin/logout [post]
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		s.sessionStrategy.Destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// requireAdminSession checks the session cookie and returns the Identity or
// writes a 401 response.
func (s *Server) requireAdminSession(w http.ResponseWriter, r *http.Request) *auth.Identity {
	id, err := s.sessionStrategy.Authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil
	}
	return id
}

// handleAdminTokens godoc
// @Summary      Manage subscription keys (admin only)
// @Description  GET lists, POST issues, PATCH updates repos/abilities, DELETE revokes.
// @Description  Requires a valid admin session cookie (obtained via /admin/login).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      TokenRequest        false  "Issue body (POST)"
// @Param        body  body      UpdateTokenRequest  false  "Update body (PATCH) — repos and/or abilities"
// @Param        body  body      RevokeTokenRequest  false  "Revoke body (DELETE)"
// @Success      200   {object}  TokenListResponse   "GET response"
// @Success      201   {object}  TokenResponse       "POST response"
// @Success      200   {object}  MessageResponse     "PATCH / DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/tokens [get]
// @Router       /admin/tokens [post]
// @Router       /admin/tokens [patch]
// @Router       /admin/tokens [delete]
func (s *Server) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminListTokens(w, r)
	case http.MethodPost:
		// Reuse the existing issuance path.
		s.issueToken(w, r)
	case http.MethodPatch:
		s.adminUpdateToken(w, r)
	case http.MethodDelete:
		s.revokeToken(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminUpdateToken(w http.ResponseWriter, r *http.Request) {
	var req UpdateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	if req.Repos == nil && req.Abilities == nil {
		writeError(w, http.StatusBadRequest, "at least one of repos, abilities must be provided")
		return
	}

	if req.Repos != nil {
		repos := normalizeRepos(*req.Repos)
		if err := s.validateRepos(repos); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.tokenStrategy.UpdateRepos(req.Token, repos); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
	}

	if req.Abilities != nil {
		abilities := normalizeAbilities(*req.Abilities)
		if err := validateAbilities(abilities); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.tokenStrategy.UpdateAbilities(req.Token, abilities); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, MessageResponse{Message: "token updated"})
}

func (s *Server) adminListTokens(w http.ResponseWriter, _ *http.Request) {
	entries := s.tokenStrategy.List()
	items := make([]TokenListItem, 0, len(entries))
	for _, e := range entries {
		// HasAccount flags tokens whose stored username resolves to a
		// live account. Scoped form ("github:peter") is normal; the
		// bare form ("alice") is the back-compat shorthand for
		// (local, alice). Either way, a false flag triggers the "Bind"
		// action on the subscriptions UI.
		provider, identifier, perr := parseTokenUsername(e.Username)
		has := perr == nil && s.accounts.Has(provider, identifier)
		items = append(items, TokenListItem{
			Token:      e.Token,
			Username:   e.Username,
			Repos:      e.Repos,
			Abilities:  e.Abilities,
			HasAccount: has,
		})
	}
	writeJSON(w, http.StatusOK, TokenListResponse{
		Tokens: items,
		Count:  len(items),
	})
}

// handleAdminBindToken godoc
// @Summary      Bind a legacy token to an account (admin only)
// @Description  Creates a local role=regular account for the token's
// @Description  username if one does not yet exist. Idempotent:
// @Description  returns 200 either way, and does nothing if the token
// @Description  already resolves to an account. See accounts.md §6.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      BindTokenRequest  true  "Bind body"
// @Success      200   {object}  AccountView
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/tokens/bind [post]
func (s *Server) handleAdminBindToken(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req BindTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	entry := s.tokenStrategy.Get(req.Token)
	if entry == nil {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	provider, identifier, err := parseTokenUsername(entry.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if acc, err := s.accounts.Get(provider, identifier); err == nil {
		// Already bound — return the existing account so the UI can
		// refresh without branching.
		writeJSON(w, http.StatusOK, s.accountView(*acc))
		return
	}
	// Bind only creates local accounts — non-local tokens can't be
	// legacy in practice (OAuth accounts are always registered via
	// the callback that mints the token's account in the first place).
	// Guard the invariant so a future scoped "github:..." token that
	// somehow lost its account row doesn't silently get a bogus row.
	if provider != accounts.ProviderLocal {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("can only bind local accounts; %q has provider %q — re-register via the OAuth flow", entry.Username, provider))
		return
	}
	stored, err := s.accounts.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: identifier,
		Role:       accounts.RoleRegular,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("server: bound legacy token (user=%q) to new regular account", entry.Username)
	writeJSON(w, http.StatusOK, s.accountView(*stored))
}

// handleAdminSession godoc
// @Summary      Current admin session
// @Description  Returns the admin identity associated with the session cookie,
// @Description  or 401 if no valid session exists. The admin UI polls this on
// @Description  load to decide whether to show the login form.
// @Tags         admin
// @Produce      json
// @Success      200  {object}  AdminLoginResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /admin/session [get]
func (s *Server) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	resp := AdminLoginResponse{Username: id.Username}
	// Enrich with display_name / role if the session's identifier
	// resolves to a local account. Sessions today are only minted for
	// local logins, so this is the common path; a missing row is fine
	// (session is still valid, the UI just sees plain username).
	if acc, err := s.accounts.Get(accounts.ProviderLocal, id.Username); err == nil {
		resp.DisplayName = acc.DisplayName
		resp.Role = acc.Role
	}
	writeJSON(w, http.StatusOK, resp)
}

