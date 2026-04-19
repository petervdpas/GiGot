package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// destinationView projects a stored Destination onto its wire shape.
func destinationView(d destinations.Destination) DestinationView {
	return DestinationView{
		ID:             d.ID,
		URL:            d.URL,
		CredentialName: d.CredentialName,
		Enabled:        d.Enabled,
		LastSyncAt:     d.LastSyncAt,
		LastSyncStatus: d.LastSyncStatus,
		LastSyncError:  d.LastSyncError,
		CreatedAt:      d.CreatedAt,
	}
}

// splitDestinationsPath pulls the {repo} and optional {id} out of a
// path of the form /api/admin/repos/{repo}/destinations[/{id}]. Returns
// empty strings for segments that aren't present.
func splitDestinationsPath(p string) (repo, id string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/admin/repos/")
	if rest == p {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	// parts: [repo, "destinations", id?]
	if len(parts) < 2 || parts[1] != "destinations" {
		return "", "", false
	}
	if parts[0] == "" {
		return "", "", false
	}
	repo = parts[0]
	if len(parts) >= 3 {
		id = parts[2]
	}
	if len(parts) > 3 {
		return "", "", false
	}
	return repo, id, true
}

// handleAdminRepoDestinations godoc
// @Summary      List or create mirror-sync destinations on a repo (admin only)
// @Description  GET lists destinations; POST adds a new one. Each
// @Description  destination points at a named credential in the vault —
// @Description  see docs/design/credential-vault.md §5 and
// @Description  docs/design/remote-sync.md §3.1. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        name  path      string                     true  "Repo name"
// @Param        body  body      CreateDestinationRequest   false "Create body (POST)"
// @Success      200   {object}  DestinationListResponse    "GET response"
// @Success      201   {object}  DestinationView            "POST response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse              "Repo not found, or credential_name unknown"
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/repos/{name}/destinations [get]
// @Router       /admin/repos/{name}/destinations [post]
func (s *Server) handleAdminRepoDestinations(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	repo, id, ok := splitDestinationsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid destinations path")
		return
	}
	if !s.git.Exists(repo) {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	if id == "" {
		switch r.Method {
		case http.MethodGet:
			s.adminListDestinations(w, r, repo)
		case http.MethodPost:
			s.adminCreateDestination(w, r, repo)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminGetDestination(w, r, repo, id)
	case http.MethodPatch:
		s.adminUpdateDestination(w, r, repo, id)
	case http.MethodDelete:
		s.adminDeleteDestination(w, r, repo, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminListDestinations(w http.ResponseWriter, _ *http.Request, repo string) {
	items := s.destinations.All(repo)
	views := make([]DestinationView, 0, len(items))
	for _, d := range items {
		views = append(views, destinationView(*d))
	}
	writeJSON(w, http.StatusOK, DestinationListResponse{
		Destinations: views,
		Count:        len(views),
	})
}

func (s *Server) adminCreateDestination(w http.ResponseWriter, r *http.Request, repo string) {
	var req CreateDestinationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.CredentialName == "" {
		writeError(w, http.StatusBadRequest, "credential_name is required")
		return
	}
	if _, err := s.credentials.Get(req.CredentialName); err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, http.StatusNotFound, "credential_name does not exist in the vault")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	stored, err := s.destinations.Add(repo, destinations.Destination{
		URL:            req.URL,
		CredentialName: req.CredentialName,
		Enabled:        enabled,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, destinationView(*stored))
}

// handleAdminRepoDestination godoc
// @Summary      Manage one destination by id (admin only)
// @Description  GET returns the destination; PATCH updates any of url/
// @Description  credential_name/enabled (omitted fields are left
// @Description  unchanged); DELETE removes it. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        name  path      string                      true  "Repo name"
// @Param        id    path      string                      true  "Destination id"
// @Param        body  body      UpdateDestinationRequest    false "Patch body (PATCH)"
// @Success      200   {object}  DestinationView
// @Success      204   "DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/repos/{name}/destinations/{id} [get]
// @Router       /admin/repos/{name}/destinations/{id} [patch]
// @Router       /admin/repos/{name}/destinations/{id} [delete]
func (s *Server) adminGetDestination(w http.ResponseWriter, _ *http.Request, repo, id string) {
	d, err := s.destinations.Get(repo, id)
	if err != nil {
		if errors.Is(err, destinations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, destinationView(*d))
}

func (s *Server) adminUpdateDestination(w http.ResponseWriter, r *http.Request, repo, id string) {
	var req UpdateDestinationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL != nil && *req.URL == "" {
		writeError(w, http.StatusBadRequest, "url cannot be empty; delete the destination instead")
		return
	}
	if req.CredentialName != nil {
		if *req.CredentialName == "" {
			writeError(w, http.StatusBadRequest, "credential_name cannot be empty; delete the destination instead")
			return
		}
		if _, err := s.credentials.Get(*req.CredentialName); err != nil {
			if errors.Is(err, credentials.ErrNotFound) {
				writeError(w, http.StatusNotFound, "credential_name does not exist in the vault")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	stored, err := s.destinations.Update(repo, id, func(d *destinations.Destination) {
		if req.URL != nil {
			d.URL = *req.URL
		}
		if req.CredentialName != nil {
			d.CredentialName = *req.CredentialName
		}
		if req.Enabled != nil {
			d.Enabled = *req.Enabled
		}
	})
	if err != nil {
		if errors.Is(err, destinations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, destinationView(*stored))
}

func (s *Server) adminDeleteDestination(w http.ResponseWriter, _ *http.Request, repo, id string) {
	if err := s.destinations.Remove(repo, id); err != nil {
		if errors.Is(err, destinations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
