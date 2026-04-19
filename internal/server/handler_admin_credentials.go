package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/credentials"
)

// credentialView maps a stored Credential to its wire-format view, stripping
// the Secret so it never leaves the server after the initial write.
func credentialView(c credentials.Credential) CredentialView {
	pv := c.PublicView()
	return CredentialView{
		Name:      pv.Name,
		Kind:      pv.Kind,
		Expires:   pv.Expires,
		LastUsed:  pv.LastUsed,
		CreatedAt: pv.CreatedAt,
		Notes:     pv.Notes,
	}
}

// handleAdminCredentials godoc
// @Summary      Manage the credential vault (admin only)
// @Description  GET lists credential metadata; POST creates a new credential.
// @Description  The secret is write-only — it is never returned on any
// @Description  response. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      CreateCredentialRequest  false  "Create body (POST)"
// @Success      200   {object}  CredentialListResponse   "GET response"
// @Success      201   {object}  CredentialView           "POST response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse            "Name already exists"
// @Router       /admin/credentials [get]
// @Router       /admin/credentials [post]
func (s *Server) handleAdminCredentials(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminListCredentials(w, r)
	case http.MethodPost:
		s.adminCreateCredential(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminCredential godoc
// @Summary      Manage one credential by name (admin only)
// @Description  GET returns metadata; PATCH updates any of kind/secret/
// @Description  expires/notes (omitted fields are left unchanged); DELETE
// @Description  removes the credential. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        name  path      string                    true  "Credential name"
// @Param        body  body      UpdateCredentialRequest   false "Patch body (PATCH)"
// @Success      200   {object}  CredentialView
// @Success      204   "DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/credentials/{name} [get]
// @Router       /admin/credentials/{name} [patch]
// @Router       /admin/credentials/{name} [delete]
func (s *Server) handleAdminCredential(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/admin/credentials/")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid credential name")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminGetCredential(w, r, name)
	case http.MethodPatch:
		s.adminUpdateCredential(w, r, name)
	case http.MethodDelete:
		s.adminDeleteCredential(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminListCredentials(w http.ResponseWriter, _ *http.Request) {
	items := s.credentials.All()
	views := make([]CredentialView, 0, len(items))
	for _, c := range items {
		views = append(views, credentialView(*c))
	}
	writeJSON(w, http.StatusOK, CredentialListResponse{
		Credentials: views,
		Count:       len(views),
	})
}

func (s *Server) adminCreateCredential(w http.ResponseWriter, r *http.Request) {
	var req CreateCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.Contains(req.Name, "/") {
		writeError(w, http.StatusBadRequest, "name must not contain /")
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	if _, err := s.credentials.Get(req.Name); err == nil {
		writeError(w, http.StatusConflict, "credential with that name already exists")
		return
	}
	stored, err := s.credentials.Put(credentials.Credential{
		Name:    req.Name,
		Kind:    req.Kind,
		Secret:  req.Secret,
		Expires: req.Expires,
		Notes:   req.Notes,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, credentialView(*stored))
}

func (s *Server) adminGetCredential(w http.ResponseWriter, _ *http.Request, name string) {
	c, err := s.credentials.Get(name)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, credentialView(*c))
}

func (s *Server) adminUpdateCredential(w http.ResponseWriter, r *http.Request, name string) {
	var req UpdateCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := s.credentials.Get(name)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Apply non-nil patch fields. Empty string in Kind or Secret is a client
	// error — to clear them, DELETE the credential instead.
	if req.Kind != nil {
		if *req.Kind == "" {
			writeError(w, http.StatusBadRequest, "kind cannot be empty; delete the credential instead")
			return
		}
		existing.Kind = *req.Kind
	}
	if req.Secret != nil {
		if *req.Secret == "" {
			writeError(w, http.StatusBadRequest, "secret cannot be empty; delete the credential instead")
			return
		}
		existing.Secret = *req.Secret
	}
	if req.Expires != nil {
		existing.Expires = req.Expires
	}
	if req.Notes != nil {
		existing.Notes = *req.Notes
	}
	stored, err := s.credentials.Put(*existing)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, credentialView(*stored))
}

func (s *Server) adminDeleteCredential(w http.ResponseWriter, _ *http.Request, name string) {
	if err := s.credentials.Remove(name); err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
