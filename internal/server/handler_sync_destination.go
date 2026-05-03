package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// Sync statuses written to Destination.LastSyncStatus. The set is small
// and stable; the admin UI renders them as coloured badges (slice 3).
const (
	syncStatusOK    = "ok"
	syncStatusError = "error"
)

// syncDestination runs one outbound mirror push for (repo, id) and
// records the outcome on the destination. Shared by the admin-session
// and subscriber-bearer routes — the only gating difference is the
// caller's middleware, not the push itself. enabled=false destinations
// still accept a manual sync: the flag gates the automatic post-receive
// fan-out (slice 2b), not explicit operator action.
//
// @Summary      Trigger a manual mirror-sync push on one destination
// @Description  Invokes `git push +refs/heads/*:refs/heads/* +refs/audit/*:refs/audit/*`
// @Description  against the destination's URL, using the vault credential
// @Description  referenced by `credential_name`. Runs synchronously — the
// @Description  response is the updated destination with `last_sync_at`,
// @Description  `last_sync_status`, and (on failure) `last_sync_error`
// @Description  populated. On success the vault credential's `last_used`
// @Description  timestamp is also touched. Destinations with enabled=false
// @Description  still accept this call (manual is explicit operator intent);
// @Description  the flag gates only the automatic post-receive fan-out.
// @Description
// @Description  Authorization mirrors the rest of /destinations*: admin
// @Description  route requires an admin session; subscriber route requires
// @Description  bearer token + repo scope + admin/maintainer role +
// @Description  `mirror` ability (see accounts.md §6.1, remote-sync.md
// @Description  §2.6 and §5).
// @Tags        destinations
// @Produce      json
// @Param        name  path      string            true  "Repo name"
// @Param        id    path      string            true  "Destination id"
// @Success      200   {object}  DestinationView
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse    "Subscriber route: missing mirror ability, regular role, or repo out of scope"
// @Failure      404   {object}  ErrorResponse    "Repo, destination, or credential (deleted) not found"
// @Failure      409   {object}  ErrorResponse    "Credential referenced by destination no longer exists in the vault"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations/{id}/sync [post]
// @Router       /repos/{name}/destinations/{id}/sync [post]
func (s *Server) syncDestination(w http.ResponseWriter, r *http.Request, repo, id string) {
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
			// The vault entry was deleted or renamed out from under the
			// destination. 409 matches the deletion-blocker's shape: the
			// destination still exists but its credential link is broken.
			writeError(w, http.StatusConflict,
				"credential "+dest.CredentialName+" is no longer in the vault")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Autofix .gitignore before the mirror push so any self-heal commit
	// rides along. No-op for non-Formidable repos and for repos whose
	// .gitignore already lists the ledger entry. Matches the post-write
	// autofix wired on POST /commits and PUT /files — the Sync-now
	// button becomes a single "bring this repo into shape and push" gesture.
	s.autofixFormidableGitignore(r, repo)

	updated, err := s.syncOnce(r.Context(), repo, dest, cred)
	if err != nil {
		// Push may have succeeded at the remote, but we couldn't record
		// the fact. Surface the storage error — the admin can retry and
		// the destination state is wrong in the store regardless.
		writeError(w, http.StatusInternalServerError, "sync recorded partial: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, destinationView(*updated))
}

// syncOnce fires one outbound push and records the outcome on the
// destination. Returns the updated destination on success, or an error
// only when the record itself could not be written. A failed push is
// NOT a syncOnce error — the destination's last_sync_status=error
// captures that, and the caller (manual Sync-now handler, automatic
// post-receive worker) decides whether to treat it as a user-visible
// failure or a silent-and-log event. On a successful push the
// credential's LastUsed timestamp is touched best-effort.
func (s *Server) syncOnce(ctx context.Context, repo string, dest *destinations.Destination, cred *credentials.Credential) (*destinations.Destination, error) {
	repoPath := s.git.RepoPath(repo)
	out, pushErr := s.pushDest(ctx, repoPath, dest.URL, cred.Secret)

	now := time.Now().UTC()
	var status, errText string
	if pushErr != nil {
		status = syncStatusError
		errText = string(out)
		if errText == "" {
			errText = pushErr.Error()
		}
	} else {
		status = syncStatusOK
	}

	updated, updErr := s.destinations.Update(repo, dest.ID, func(d *destinations.Destination) {
		t := now
		d.LastSyncAt = &t
		d.LastSyncStatus = status
		d.LastSyncError = errText
	})
	if updErr != nil {
		return nil, updErr
	}

	if pushErr == nil {
		// Best-effort bookkeeping for "last used 2 days ago" in the
		// credentials UI. Touch failure is not a sync failure.
		_ = s.credentials.Touch(dest.CredentialName)
	}
	return updated, nil
}
