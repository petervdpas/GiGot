package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/audit"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/tags"
)

// tagView packages a stored Tag plus its direct usage counts into the
// wire format. Caller is responsible for passing in the usage map
// from store.Usage() (one map lookup per tag, computed once per
// list/get call).
func tagView(t tags.Tag, u tags.UsageCounts) TagView {
	return TagView{
		ID:        t.ID,
		Name:      t.Name,
		CreatedAt: t.CreatedAt,
		CreatedBy: t.CreatedBy,
		Usage: TagUsage{
			Repos:         u.Repos,
			Subscriptions: u.Subscriptions,
			Accounts:      u.Accounts,
		},
	}
}

// auditActorFromIdentity translates a session Identity into an audit
// Actor for the system log. Mirrors what git.AppendAudit accepts so
// the two audit surfaces stay symmetric.
func auditActorFromIdentity(id *auth.Identity) audit.Actor {
	if id == nil {
		return audit.Actor{}
	}
	return audit.Actor{
		ID:       id.ID,
		Username: id.Username,
		Provider: id.AccountProvider,
	}
}

// handleAdminTags godoc
// @Summary      Manage the tag catalogue (admin only)
// @Description  GET lists every tag with direct usage counts; POST
// @Description  creates a new tag. Names are case-insensitive unique.
// @Description  Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      CreateTagRequest        false  "Create body (POST)"
// @Success      200   {object}  TagListResponse         "GET response"
// @Success      201   {object}  TagView                 "POST response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse           "Name already exists"
// @Security    SessionAuth
// @Router       /admin/tags [get]
// @Router       /admin/tags [post]
func (s *Server) handleAdminTags(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminListTags(w, r)
	case http.MethodPost:
		s.adminCreateTag(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminTagsSweepUnused godoc
// @Summary      Sweep unused tags (admin only)
// @Description  Deletes every catalogue row that has zero assignments
// @Description  across repos, subscriptions, and accounts. Each
// @Description  removed row emits a `tag.deleted` system audit event
// @Description  so the chain answers "where did `team:archived` go?"
// @Description  the same way a one-by-one delete would. The response
// @Description  carries the names that were removed so the UI can
// @Description  show a "swept N tags" summary.
// @Tags         admin
// @Produce      json
// @Success      200   {object}  TagSweepUnusedResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security    SessionAuth
// @Router       /admin/tags/sweep-unused [post]
func (s *Server) handleAdminTagsSweepUnused(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	removed, err := s.tags.DeleteUnused()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	names := make([]string, 0, len(removed))
	for _, t := range removed {
		// Per-row audit event matches the single-delete path so a
		// forensic reader doesn't see two different event shapes for
		// "this tag is gone."
		s.recordSystemAudit("tag.deleted", id, map[string]any{
			"id":   t.ID,
			"name": t.Name,
			"swept": map[string]int{
				"repos":         0,
				"subscriptions": 0,
				"accounts":      0,
			},
			"reason": "unused-sweep",
		})
		names = append(names, t.Name)
	}
	writeJSON(w, http.StatusOK, TagSweepUnusedResponse{
		Removed: names,
		Count:   len(names),
	})
}

// handleAdminTag godoc
// @Summary      Manage one tag by ID (admin only)
// @Description  PATCH renames the tag (case-insensitive unique). DELETE
// @Description  cascades through every assignment join — repo, subscription,
// @Description  and account — and returns the per-set sweep counts.
// @Description  Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        id    path      string             true   "Tag ID"
// @Param        body  body      RenameTagRequest   false  "Patch body (PATCH)"
// @Success      200   {object}  TagView
// @Success      200   {object}  TagDeleteResponse  "DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse      "Rename collides with another tag"
// @Security    SessionAuth
// @Router       /admin/tags/{id} [patch]
// @Router       /admin/tags/{id} [delete]
func (s *Server) handleAdminTag(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	tagID := strings.TrimPrefix(r.URL.Path, "/api/admin/tags/")
	if tagID == "" || strings.Contains(tagID, "/") {
		writeError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.adminRenameTag(w, r, id, tagID)
	case http.MethodDelete:
		s.adminDeleteTag(w, r, id, tagID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminListTags(w http.ResponseWriter, _ *http.Request) {
	all := s.tags.All()
	usage := s.tags.Usage()
	views := make([]TagView, 0, len(all))
	for _, t := range all {
		views = append(views, tagView(*t, usage[t.ID]))
	}
	writeJSON(w, http.StatusOK, TagListResponse{
		Tags:  views,
		Count: len(views),
	})
}

func (s *Server) adminCreateTag(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	var req CreateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	created, err := s.tags.Create(req.Name, id.Username)
	if err != nil {
		switch {
		case errors.Is(err, tags.ErrNameRequired):
			writeError(w, http.StatusBadRequest, "name is required")
		case errors.Is(err, tags.ErrNameInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, tags.ErrNameDuplicate):
			writeError(w, http.StatusConflict, "tag with that name already exists")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.recordSystemAudit("tag.created", id, map[string]any{
		"id":   created.ID,
		"name": created.Name,
	})
	writeJSON(w, http.StatusCreated, tagView(*created, tags.UsageCounts{}))
}

func (s *Server) adminRenameTag(w http.ResponseWriter, r *http.Request, id *auth.Identity, tagID string) {
	var req RenameTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	before, err := s.tags.Get(tagID)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	renamed, err := s.tags.Rename(tagID, req.Name)
	if err != nil {
		switch {
		case errors.Is(err, tags.ErrNotFound):
			writeError(w, http.StatusNotFound, "tag not found")
		case errors.Is(err, tags.ErrNameRequired):
			writeError(w, http.StatusBadRequest, "name is required")
		case errors.Is(err, tags.ErrNameInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, tags.ErrNameDuplicate):
			writeError(w, http.StatusConflict, "tag with that name already exists")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.recordSystemAudit("tag.renamed", id, map[string]any{
		"id":       renamed.ID,
		"old_name": before.Name,
		"new_name": renamed.Name,
	})
	usage := s.tags.Usage()
	writeJSON(w, http.StatusOK, tagView(*renamed, usage[renamed.ID]))
}

func (s *Server) adminDeleteTag(w http.ResponseWriter, _ *http.Request, id *auth.Identity, tagID string) {
	before, err := s.tags.Get(tagID)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	swept, err := s.tags.Delete(tagID)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSystemAudit("tag.deleted", id, map[string]any{
		"id":   before.ID,
		"name": before.Name,
		"swept": map[string]int{
			"repos":         swept.RepoAssignments,
			"subscriptions": swept.SubscriptionAssignments,
			"accounts":      swept.AccountAssignments,
		},
	})
	writeJSON(w, http.StatusOK, TagDeleteResponse{
		Deleted: tagView(*before, tags.UsageCounts{}),
		Swept: TagUsage{
			Repos:         swept.RepoAssignments,
			Subscriptions: swept.SubscriptionAssignments,
			Accounts:      swept.AccountAssignments,
		},
	})
}

