package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// pullOnce fires one inbound fetch and reports outcome to the caller.
// Mirrors pushOnce in shape but deliberately does NOT touch LastSync*
// — those fields belong to the push direction (the "Last sync" badge
// in the admin UI reads "when did we last push?"). On success we still
// mark the remote as in-sync because a force-pull, by construction,
// leaves local refs equal to whatever the remote holds; that flips
// the DIVERGED badge the operator was acting on.
func (s *Server) pullOnce(ctx context.Context, repo string, dest *destinations.Destination, cred *credentials.Credential) ([]byte, error) {
	repoPath := s.git.RepoPath(repo)
	out, fetchErr := s.pullDest(ctx, repoPath, dest.URL, cred.Secret)
	if fetchErr != nil {
		return out, fetchErr
	}
	// Best-effort bookkeeping for "last used 2 days ago" in the
	// credentials UI. Touch failure is not a pull failure.
	_ = s.credentials.Touch(dest.CredentialName)
	// Local now equals remote by construction (force-fetch into the
	// mirrored namespaces); record the inferred in_sync state so the
	// admin UI's badge drops DIVERGED without waiting for the next
	// status poll. A subsequent refresh will replace this with an
	// authoritative read.
	s.markRemoteInSync(repo, dest.ID)
	return out, nil
}

// pullDestination runs one inbound mirror fetch for (repo, id) and
// returns the updated destination view on success. Admin-only by
// router placement (handleAdminRepoDestinations gates with
// requireAdminSession); not exposed on the subscriber-facing
// /api/repos/.../destinations/.../pull surface, by design.
//
// @Summary      Admin-only: pull from a mirror destination into local
// @Description  Force-fetches `refs/heads/*` and `refs/audit/*` from the
// @Description  destination URL into the local bare repo, using the
// @Description  vault credential referenced by `credential_name`. Local
// @Description  refs are overwritten to match the remote — the operator
// @Description  is explicitly accepting the remote's state as truth.
// @Description  Runs synchronously; the response is the updated
// @Description  destination view with `remote_status=in_sync` (since
// @Description  local now equals remote by construction).
// @Description
// @Description  This endpoint exists ONLY on the admin route. There is
// @Description  no subscriber-facing variant: pulling is destructive to
// @Description  GiGot's local refs and is reserved for the operator
// @Description  who configured the destination. See remote-sync.md §3.2.
// @Tags        destinations
// @Produce      json
// @Param        name  path      string            true  "Repo name"
// @Param        id    path      string            true  "Destination id"
// @Success      200   {object}  DestinationView
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse    "Repo, destination, or credential not found"
// @Failure      409   {object}  ErrorResponse    "Credential referenced by destination no longer exists in the vault"
// @Failure      502   {object}  ErrorResponse    "Fetch from destination failed"
// @Security     SessionAuth
// @Router       /admin/repos/{name}/destinations/{id}/pull [post]
func (s *Server) pullDestination(w http.ResponseWriter, r *http.Request, repo, id string) {
	dest, err := s.destinations.Get(repo, id)
	if err != nil {
		if errors.Is(err, destinations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cred, err := s.credentials.Get(dest.CredentialName)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, http.StatusConflict,
				"credential "+dest.CredentialName+" is no longer in the vault")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if out, err := s.pullOnce(r.Context(), repo, dest, cred); err != nil {
		// 502 — the local server is fine, but the upstream
		// destination is what failed. Body carries redacted git
		// output so the operator can see why.
		msg := string(out)
		if msg == "" {
			msg = err.Error()
		}
		writeError(w, http.StatusBadGateway, "pull failed: "+msg)
		return
	}

	// Re-fetch to surface the just-marked in_sync remote state.
	fresh, err := s.destinations.Get(repo, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, destinationView(*fresh))
}
