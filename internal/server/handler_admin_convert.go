package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// ConvertFormidableResponse's Added field lists the scaffold paths a
// successful convert actually wrote (marker, templates/basic.yaml,
// storage/.gitkeep — any subset of these). Empty on an idempotent
// re-invocation where every piece was already in place.

// ConvertFormidableResponse is the 200 body from
// POST /api/admin/repos/{name}/formidable. Stamped distinguishes a
// first-time or partial conversion (true — at least one scaffold
// piece was written) from an idempotent re-invocation on a repo that
// already carried a valid marker, a templates/ entry, and a storage/
// entry (false — no commit written). Added lists the exact paths the
// commit wrote (marker, templates/basic.yaml, storage/.gitkeep, or
// any subset) so the admin UI can surface which pieces were missing.
// Repo carries the enriched RepoInfo so the UI can refresh the card
// in place.
type ConvertFormidableResponse struct {
	Stamped bool     `json:"stamped"`
	Added   []string `json:"added,omitempty"`
	Repo    RepoInfo `json:"repo"`
}

// splitConvertPath pulls {name} out of /api/admin/repos/{name}/formidable.
// Returns ("", false) for any shape that isn't exactly that.
func splitConvertPath(p string) (string, bool) {
	rest := strings.TrimPrefix(p, "/api/admin/repos/")
	if rest == p {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "formidable" || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

// handleAdminRepoSub dispatches /api/admin/repos/{name}/... to the
// right sub-handler based on the first segment after {name}/.
// Unknown subroutes get a 404 rather than falling through to one of
// the specific handlers, which would 400 with a misleading message.
func (s *Server) handleAdminRepoSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/repos/")
	if rest == r.URL.Path {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.SplitN(rest, "/", 3)
	// parts: [repo, subresource, ...]
	if len(parts) < 2 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid repo subpath")
		return
	}
	switch parts[1] {
	case "destinations":
		s.handleAdminRepoDestinations(w, r)
	case "formidable":
		s.handleAdminConvertFormidable(w, r)
	case "tags":
		s.handleAdminRepoTags(w, r)
	default:
		writeError(w, http.StatusNotFound, "unknown repo subroute")
	}
}

// handleAdminConvertFormidable godoc
// @Summary      Convert a plain repo to a Formidable context (admin only)
// @Description  Stamps .formidable/context.json on top of HEAD so the
// @Description  repo picks up structured record-merge behaviour on subsequent
// @Description  writes. Gated to server.formidable_first=true so generic-mode
// @Description  operators don't trip this accidentally. Idempotent: a repo
// @Description  that already carries a valid marker returns stamped=false
// @Description  and writes no commit. On a successful stamp the server
// @Description  appends one `repo_convert_formidable` audit entry.
// @Tags         admin
// @Produce      json
// @Param        name  path  string  true  "Repo name"
// @Success      200   {object}  ConvertFormidableResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse  "Server not in formidable_first mode"
// @Failure      404   {object}  ErrorResponse  "Repo not found"
// @Failure      405   {object}  ErrorResponse
// @Failure      422   {object}  ErrorResponse  "Empty repo — nothing to stamp on top of"
// @Router       /admin/repos/{name}/formidable [post]
func (s *Server) handleAdminConvertFormidable(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, ok := splitConvertPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid convert path")
		return
	}
	if !s.cfg.Server.FormidableFirst {
		writeError(w, http.StatusForbidden,
			"convert-to-formidable requires server.formidable_first=true")
		return
	}
	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	// ensureFormidableShape writes on top of HEAD, so a repo with no
	// commits has nothing to build on. 422 is the right signal — the
	// request shape is fine, the repo state isn't.
	if _, err := s.git.Head(name); err != nil {
		if errors.Is(err, gitmanager.ErrRepoEmpty) {
			writeError(w, http.StatusUnprocessableEntity,
				"repo is empty — create with scaffold_formidable:true instead")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	added, err := ensureFormidableShape(s.git, name, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(added) > 0 {
		notes := "converted " + name + " to Formidable context: added " +
			strings.Join(added, ", ")
		s.appendAudit(name, gitmanager.AuditEvent{
			Type:  AuditTypeRepoConvertFormidable,
			Actor: auditActor(r),
			Notes: notes,
		})
	}

	writeJSON(w, http.StatusOK, ConvertFormidableResponse{
		Stamped: len(added) > 0,
		Added:   added,
		Repo:    s.repoInfo(name),
	})
}
