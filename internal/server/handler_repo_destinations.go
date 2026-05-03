package server

import (
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/policy"
)

// splitRepoDestinationsPath pulls the {repo}, optional {id}, and
// optional trailing action out of a path of the form
// /api/repos/{repo}/destinations[/{id}[/{action}]]. Mirrors
// splitDestinationsPath but for the subscriber-facing route — the
// admin path has the /api/admin/ prefix, this one does not.
func splitRepoDestinationsPath(p string) (repo, id, action string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/repos/")
	if rest == p {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
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

// handleRepoDestinations godoc
// @Summary      Manage mirror-sync destinations on a repo (subscriber)
// @Description  Subscriber-facing counterpart to /api/admin/repos/{name}/destinations.
// @Description  Three-layer gate (see accounts.md §6.1, remote-sync.md §2.6):
// @Description
// @Description    1. TokenRepoPolicy — repo in the bearer token's allowlist.
// @Description    2. requireMaintainerOrAdmin — issuing account's role is
// @Description       admin or maintainer; regular accounts are denied even
// @Description       if their key carries the `mirror` ability bit (the
// @Description       role is a structural fence on top of per-token bits).
// @Description    3. TokenAbilityPolicy("mirror") — the per-key opt-in.
// @Description
// @Description  Any layer denying writes 403. The admin-session route at
// @Description  /api/admin/repos/{name}/destinations remains the override
// @Description  for full server administration.
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
// @Failure      403   {object}  ErrorResponse              "Missing mirror ability, regular role, or repo out of scope"
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/destinations [get]
// @Router       /repos/{name}/destinations [post]
// @Router       /repos/{name}/destinations/{id} [get]
// @Router       /repos/{name}/destinations/{id} [patch]
// @Router       /repos/{name}/destinations/{id} [delete]
func (s *Server) handleRepoDestinations(w http.ResponseWriter, r *http.Request) {
	repo, id, action, ok := splitRepoDestinationsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid destinations path")
		return
	}
	// Read vs. write split: GETs are informational and only need
	// repo-scope read access — a regular subscriber may want to see
	// which mirrors are configured even though they can't manage
	// them. Writes (POST/PATCH/DELETE and the /sync action) keep the
	// full three-gate stack (write-scope policy + maintainer-or-admin
	// role + the per-key `mirror` ability), so the role and ability
	// fences still hold for anything that mutates state or triggers
	// an outbound push.
	isWrite := r.Method != http.MethodGet
	if isWrite {
		if !s.requireAllow(w, r, policy.ActionWriteRepo, repo) {
			return
		}
		if !s.requireMaintainerOrAdmin(w, r) {
			return
		}
		if !s.requireAbility(w, r, auth.AbilityMirror) {
			return
		}
	} else {
		if !s.requireAllow(w, r, policy.ActionReadRepo, repo) {
			return
		}
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
