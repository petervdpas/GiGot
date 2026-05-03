package server

import (
	"errors"
	"net/http"

	"github.com/petervdpas/GiGot/internal/destinations"
)

// refreshDestinationStatus runs one outbound `git ls-remote` against
// the destination's URL, compares against the local mirrored refs,
// and returns the updated destination view (RemoteStatus,
// RemoteCheckedAt, RemoteCheckError, RemoteRefs populated). Shared by
// the admin-session and subscriber-bearer routes.
//
// Authorization mirrors /sync (see remote-sync.md §2.6 + accounts.md
// §6.1): admin session bypasses; subscriber needs repo scope +
// admin/maintainer role + `mirror` ability. Manual refresh is
// explicit operator intent so disabled destinations are still
// allowed (matches the /sync handler's behaviour).
//
// @Summary      Refresh a mirror destination's remote-status
// @Description  Invokes `git ls-remote --refs` against the destination's URL
// @Description  using the vault credential referenced by `credential_name`,
// @Description  compares the result against local refs/heads/* + refs/audit/*,
// @Description  and writes the outcome onto the destination as
// @Description  `remote_status`, `remote_checked_at`, optional
// @Description  `remote_check_error`, and per-ref `remote_refs[]`. Returns the
// @Description  updated destination view. No push is performed — this is the
// @Description  read-side companion to /sync. Disabled destinations still
// @Description  accept this call (manual is explicit operator intent); the
// @Description  flag gates only the post-receive auto-mirror.
// @Tags        destinations
// @Produce      json
// @Param        name  path      string            true  "Repo name"
// @Param        id    path      string            true  "Destination id"
// @Success      200   {object}  DestinationView
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse    "Subscriber route: missing mirror ability, regular role, or repo out of scope"
// @Failure      404   {object}  ErrorResponse    "Repo, destination, or credential (deleted) not found"
// @Failure      409   {object}  ErrorResponse    "Credential referenced by destination no longer exists in the vault"
// @Failure      502   {object}  ErrorResponse    "ls-remote failed (auth/network); the destination is still updated with status=error so the admin UI badge reflects it"
// @Security     SessionAuth
// @Security     BearerAuth
// @Router       /admin/repos/{name}/destinations/{id}/status/refresh [post]
// @Router       /repos/{name}/destinations/{id}/status/refresh [post]
func (s *Server) refreshDestinationStatus(w http.ResponseWriter, r *http.Request, repo, id string) {
	// Sanity-check existence up front so a missing destination /
	// credential returns 404 / 409 with the same shape as the /sync
	// handler. refreshRemoteStatus also fetches them, but doing it
	// here means we can map the error before the side-effect runs.
	dest, err := s.destinations.Get(repo, id)
	if err != nil {
		if errors.Is(err, destinations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.refreshRemoteStatus(r.Context(), repo, id); err != nil {
		// refreshRemoteStatus has already written status=error onto the
		// destination, so the admin UI badge is correct regardless. We
		// still return 502 here so the manual-refresh flow surfaces the
		// problem in the moment rather than only via the badge.
		writeError(w, http.StatusBadGateway, "ls-remote failed: "+err.Error())
		// Fall through? No — returning the destination view on the
		// error path would imply success. The fresh state lands in the
		// next list/get call.
		_ = dest
		return
	}

	updated, err := s.destinations.Get(repo, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, destinationView(*updated))
}
