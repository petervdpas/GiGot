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

// splitDestinationsPath pulls the {repo}, optional {id}, and optional
// trailing action out of a path of the form
// /api/admin/repos/{repo}/destinations[/{id}[/{action}]]. Returns empty
// strings for segments that aren't present. Currently the only action
// recognised is "sync"; unknown actions still return ok=true and the
// dispatcher decides what to do with them (so a 404 surfaces the
// typo rather than a misleading 400).
func splitDestinationsPath(p string) (repo, id, action string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/admin/repos/")
	if rest == p {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	// parts: [repo, "destinations", id?, action?]
	if len(parts) < 2 || parts[1] != "destinations" {
		return "", "", "", false
	}
	if parts[0] == "" {
		return "", "", "", false
	}
	repo = parts[0]
	if len(parts) >= 3 {
		id = parts[2]
	}
	if len(parts) >= 4 {
		action = parts[3]
	}
	if len(parts) > 4 {
		return "", "", "", false
	}
	return repo, id, action, true
}

// handleAdminRepoDestinations dispatches /api/admin/repos/{name}/destinations
// (collection) and /api/admin/repos/{name}/destinations/{id}[/sync]
// (single + action) by method. Per-operation godoc lives on the helper
// functions below (listDestinations, createDestination, getDestination,
// updateDestination, deleteDestination) so the swagger spec describes
// each verb in isolation rather than smearing one shared description
// across five operations.
func (s *Server) handleAdminRepoDestinations(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	repo, id, action, ok := splitDestinationsPath(r.URL.Path)
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
			s.listDestinations(w, r, repo)
		case http.MethodPost:
			s.createDestination(w, r, repo)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if action != "" {
		if action == "sync" && r.Method == http.MethodPost {
			s.syncDestination(w, r, repo, id)
			return
		}
		writeError(w, http.StatusNotFound, "unknown destination action")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getDestination(w, r, repo, id)
	case http.MethodPatch:
		s.updateDestination(w, r, repo, id)
	case http.MethodDelete:
		s.deleteDestination(w, r, repo, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listDestinations godoc
// @Summary      List mirror-sync destinations on a repo
// @Description  Returns every destination attached to the named repo,
// @Description  with last-sync status fields populated. Read-only;
// @Description  the per-bearer mirror ability is NOT required — repo
// @Description  scope alone is sufficient (see remote-sync.md §2.6,
// @Description  the read/write split). Admin-session callers reach
// @Description  the same handler via the /api/admin/* path.
// @Tags         admin
// @Tags         repos
// @Produce      json
// @Param        name  path      string                   true  "Repo name"
// @Success      200   {object}  DestinationListResponse
// @Failure      401   {object}  ErrorResponse            "Missing session cookie / bearer token"
// @Failure      403   {object}  ErrorResponse            "Subscriber: token not in scope for this repo"
// @Failure      404   {object}  ErrorResponse            "Repo not found"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations [get]
// @Router       /repos/{name}/destinations [get]
func (s *Server) listDestinations(w http.ResponseWriter, _ *http.Request, repo string) {
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

// createDestination godoc
// @Summary      Add a mirror-sync destination to a repo
// @Description  Attaches a new destination pointing at a named credential
// @Description  in the vault (see credential-vault.md §5,
// @Description  remote-sync.md §3.1). Returns 201 with the stored
// @Description  destination on success. Subscriber callers must hold
// @Description  admin/maintainer role AND the `mirror` ability — see
// @Description  accounts.md §6.1; admin-session callers bypass.
// @Tags         admin
// @Tags         repos
// @Accept       json
// @Produce      json
// @Param        name  path      string                     true   "Repo name"
// @Param        body  body      CreateDestinationRequest   true   "Destination payload"
// @Success      201   {object}  DestinationView
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse              "Missing session cookie / bearer token"
// @Failure      403   {object}  ErrorResponse              "Subscriber: missing mirror ability or role, or out of scope"
// @Failure      404   {object}  ErrorResponse              "Repo or credential_name not found"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations [post]
// @Router       /repos/{name}/destinations [post]
func (s *Server) createDestination(w http.ResponseWriter, r *http.Request, repo string) {
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

// getDestination godoc
// @Summary      Read one mirror-sync destination by id
// @Description  Returns the destination's stored configuration plus its
// @Description  most recent sync attempt (status / timestamp / error).
// @Description  Read-only; per-bearer mirror ability NOT required —
// @Description  the read/write split lets any in-scope subscriber
// @Description  inspect the configuration without managing it.
// @Tags         admin
// @Tags         repos
// @Produce      json
// @Param        name  path      string             true  "Repo name"
// @Param        id    path      string             true  "Destination id"
// @Success      200   {object}  DestinationView
// @Failure      401   {object}  ErrorResponse      "Missing session cookie / bearer token"
// @Failure      403   {object}  ErrorResponse      "Subscriber: token not in scope for this repo"
// @Failure      404   {object}  ErrorResponse      "Repo or destination not found"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations/{id} [get]
// @Router       /repos/{name}/destinations/{id} [get]
func (s *Server) getDestination(w http.ResponseWriter, _ *http.Request, repo, id string) {
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

// updateDestination godoc
// @Summary      Patch one mirror-sync destination
// @Description  Updates any of `url`, `credential_name`, `enabled`.
// @Description  Omitted fields are left unchanged (nil-pointer ==
// @Description  "no change"). Empty-string url or credential_name is
// @Description  rejected — delete the destination instead. Subscriber
// @Description  callers need admin/maintainer role + `mirror` ability;
// @Description  admin-session callers bypass.
// @Tags         admin
// @Tags         repos
// @Accept       json
// @Produce      json
// @Param        name  path      string                     true   "Repo name"
// @Param        id    path      string                     true   "Destination id"
// @Param        body  body      UpdateDestinationRequest   true   "Patch payload"
// @Success      200   {object}  DestinationView
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse              "Missing session cookie / bearer token"
// @Failure      403   {object}  ErrorResponse              "Subscriber: missing mirror ability or role, or out of scope"
// @Failure      404   {object}  ErrorResponse              "Repo, destination, or credential_name not found"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations/{id} [patch]
// @Router       /repos/{name}/destinations/{id} [patch]
func (s *Server) updateDestination(w http.ResponseWriter, r *http.Request, repo, id string) {
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

// deleteDestination godoc
// @Summary      Remove one mirror-sync destination
// @Description  Removes the destination from the repo; the auto-mirror
// @Description  fan-out stops firing for it on subsequent commits.
// @Description  Returns 204 with no body. Subscriber callers need
// @Description  admin/maintainer role + `mirror` ability; admin-session
// @Description  callers bypass.
// @Tags         admin
// @Tags         repos
// @Produce      json
// @Param        name  path  string  true  "Repo name"
// @Param        id    path  string  true  "Destination id"
// @Success      204   "Destination removed"
// @Failure      401   {object}  ErrorResponse  "Missing session cookie / bearer token"
// @Failure      403   {object}  ErrorResponse  "Subscriber: missing mirror ability or role, or out of scope"
// @Failure      404   {object}  ErrorResponse  "Repo or destination not found"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations/{id} [delete]
// @Router       /repos/{name}/destinations/{id} [delete]
func (s *Server) deleteDestination(w http.ResponseWriter, _ *http.Request, repo, id string) {
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
