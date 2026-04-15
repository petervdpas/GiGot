package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/petervdpas/GiGot/internal/admins"
	"github.com/petervdpas/GiGot/internal/auth"
)

// handleAdminLogin godoc
// @Summary      Admin login
// @Description  Exchanges username/password for a session cookie.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      AdminLoginRequest true "Credentials"
// @Success      200   {object}  AdminLoginResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /admin/login [post]
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := s.admins.Verify(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, admins.ErrNotFound) || errors.Is(err, admins.ErrInvalidPassword) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sess, err := s.sessionStrategy.Create(a.Username)
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
		Username: a.Username,
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
// @Description  GET lists, POST issues, DELETE revokes. Requires a valid
// @Description  admin session cookie (obtained via /admin/login).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      TokenRequest             false  "Issue body (POST)"
// @Param        body  body      UpdateTokenReposRequest  false  "Update-repos body (PATCH)"
// @Param        body  body      RevokeTokenRequest       false  "Revoke body (DELETE)"
// @Success      200   {object}  TokenListResponse  "GET / PATCH response"
// @Success      201   {object}  TokenResponse      "POST response"
// @Success      200   {object}  MessageResponse    "DELETE / PATCH response"
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
		s.adminUpdateTokenRepos(w, r)
	case http.MethodDelete:
		s.revokeToken(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminUpdateTokenRepos(w http.ResponseWriter, r *http.Request) {
	var req UpdateTokenReposRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	repos := normalizeRepos(req.Repos)
	if err := s.validateRepos(repos); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.tokenStrategy.UpdateRepos(req.Token, repos); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, MessageResponse{Message: "repos updated"})
}

func (s *Server) adminListTokens(w http.ResponseWriter, _ *http.Request) {
	entries := s.tokenStrategy.List()
	items := make([]TokenListItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, TokenListItem{
			Token:    e.Token,
			Username: e.Username,
			Repos:    e.Repos,
		})
	}
	writeJSON(w, http.StatusOK, TokenListResponse{
		Tokens: items,
		Count:  len(items),
	})
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
	writeJSON(w, http.StatusOK, AdminLoginResponse{
		Username: id.Username,
	})
}

