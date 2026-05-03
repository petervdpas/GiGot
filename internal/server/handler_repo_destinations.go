package server

import (
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/policy"
)

// splitRepoDestinationsPath pulls the {repo}, optional {id}, and
// optional trailing action out of a path of the form
// /api/repos/{repo}/destinations[/{id}[/{action}[/{subaction}]]].
// Mirrors splitDestinationsPath but for the subscriber-facing route —
// the admin path has the /api/admin/ prefix, this one does not.
// Two-segment actions are joined with "/" so the dispatcher keeps a
// single string switch (e.g. "status/refresh").
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
	if len(parts) == 4 {
		action = parts[3]
	}
	if len(parts) == 5 {
		action = parts[3] + "/" + parts[4]
	}
	if len(parts) > 5 {
		return "", "", "", false
	}
	return repo, id, action, true
}

// handleRepoDestinations dispatches /api/repos/{name}/destinations
// (collection) and /api/repos/{name}/destinations/{id}[/sync] (single
// + action) by method. Per-operation godoc lives on the helper
// functions in handler_admin_destinations.go (listDestinations,
// createDestination, getDestination, updateDestination,
// deleteDestination) — the same helpers are reused for the admin
// route, so each godoc carries dual @Router lines for both.
//
// Three-layer gate for writes (see accounts.md §6.1,
// remote-sync.md §2.6):
//
//  1. TokenRepoPolicy — repo in the bearer token's allowlist.
//  2. requireMaintainerOrAdmin — issuing account's role is admin or
//     maintainer; regular accounts are denied even if their key
//     carries the `mirror` ability bit (the role is a structural
//     fence on top of per-token bits).
//  3. TokenAbilityPolicy("mirror") — the per-key opt-in.
//
// Reads (GET) require only the first layer — the read/write split
// lets any in-scope subscriber inspect destinations without the
// mirror ability.
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
		if r.Method == http.MethodPost {
			switch action {
			case "sync":
				s.syncDestination(w, r, repo, id)
				return
			case "status/refresh":
				s.refreshDestinationStatus(w, r, repo, id)
				return
			}
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
