package server

import (
	"encoding/json"
	"net/http"
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

	token, err := s.tokenStrategy.Issue(req.Username, req.Roles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, TokenResponse{
		Token:    token,
		Username: req.Username,
		Roles:    req.Roles,
	})
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
