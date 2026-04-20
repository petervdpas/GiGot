package server

import (
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/policy"
)

// splitRepoDestinationsPath pulls the {repo} and optional {id} out of a
// path of the form /api/repos/{repo}/destinations[/{id}]. Mirrors
// splitDestinationsPath but for the subscriber-facing route — the
// admin path has the /api/admin/ prefix, this one does not.
func splitRepoDestinationsPath(p string) (repo, id string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/repos/")
	if rest == p {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
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

// handleRepoDestinations godoc
// @Summary      Manage mirror-sync destinations on a repo (subscriber)
// @Description  Subscriber-facing counterpart to /api/admin/repos/{name}/destinations.
// @Description  Bearer-authenticated, gated by both TokenRepoPolicy (repo in
// @Description  the token's allowlist) and TokenAbilityPolicy("mirror")
// @Description  (see remote-sync.md §2.6). A token without the mirror
// @Description  ability receives 403 here; the admin-session route remains
// @Description  available as an override.
// @Tags         repos
// @Accept       json
// @Produce      json
// @Param        name  path      string                     true  "Repo name"
// @Param        id    path      string                     false "Destination id"
// @Param        body  body      CreateDestinationRequest   false "Create body (POST)"
// @Param        body  body      UpdateDestinationRequest   false "Patch body (PATCH)"
// @Success      200   {object}  DestinationListResponse    "GET list response"
// @Success      201   {object}  DestinationView            "POST response"
// @Success      200   {object}  DestinationView            "GET/PATCH response"
// @Success      204   "DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse              "Missing mirror ability or repo out of scope"
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/destinations [get]
// @Router       /repos/{name}/destinations [post]
// @Router       /repos/{name}/destinations/{id} [get]
// @Router       /repos/{name}/destinations/{id} [patch]
// @Router       /repos/{name}/destinations/{id} [delete]
func (s *Server) handleRepoDestinations(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := splitRepoDestinationsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid destinations path")
		return
	}
	// Gate order matters for informativeness: if the token isn't scoped
	// to the repo, that's the more specific failure to report.
	if !s.requireAllow(w, r, policy.ActionWriteRepo, repo) {
		return
	}
	if !s.requireAbility(w, r, auth.AbilityMirror) {
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
