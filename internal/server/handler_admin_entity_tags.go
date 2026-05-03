package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/audit"
	"github.com/petervdpas/GiGot/internal/auth"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/tags"
)

// SetEntityTagsRequest is the body of PUT /api/admin/repos/{name}/tags
// and PUT /api/admin/accounts/{provider}/{identifier}/tags. The tag
// list replaces the entity's direct tag set wholesale; unknown tag
// names are auto-created in the catalogue. To clear all tags, pass
// an empty array (an explicit `[]`, not a missing field).
type SetEntityTagsRequest struct {
	Tags []string `json:"tags"`
}

// EntityTagsResponse echoes back the current direct tag list after a
// PUT, so the client picker can re-render without a follow-up GET.
// Inheritance does NOT come back here — repos and accounts have no
// upstream sources, and subscription effective tags ride on the
// /admin/tokens responses (handler_admin.go) instead of here.
type EntityTagsResponse struct {
	Tags []string `json:"tags"`
}

// handleAdminRepoTags godoc
// @Summary      Manage tags on one repo (admin only)
// @Description  GET returns the direct tag list; PUT replaces it
// @Description  wholesale. Unknown tag names in the PUT body are
// @Description  auto-created in the catalogue. Each diff is recorded
// @Description  on the repo's refs/audit/main as one
// @Description  tag.assigned.repo / tag.unassigned.repo event per
// @Description  changed assignment. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        name  path      string                 true  "Repo name"
// @Param        body  body      SetEntityTagsRequest   false "PUT body"
// @Success      200   {object}  EntityTagsResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security    SessionAuth
// @Router       /admin/repos/{name}/tags [get]
// @Router       /admin/repos/{name}/tags [put]
func (s *Server) handleAdminRepoTags(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	name, ok := splitRepoTagsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid repo path")
		return
	}
	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, EntityTagsResponse{Tags: s.tags.TagsFor(tags.ScopeRepo, name)})
	case http.MethodPut:
		s.adminSetRepoTags(w, r, id, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminAccountTags godoc
// @Summary      Manage tags on one account (admin only)
// @Description  GET returns the direct tag list; PUT replaces it.
// @Description  Account tags propagate to every subscription the
// @Description  account holds (effective_tags on the token list
// @Description  responses unions account + repo + sub tags).
// @Description  Catalogue + assignment diffs land in the system
// @Description  audit log. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        provider    path      string                 true  "Account provider"
// @Param        identifier  path      string                 true  "Account identifier"
// @Param        body        body      SetEntityTagsRequest   false "PUT body"
// @Success      200   {object}  EntityTagsResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security    SessionAuth
// @Router       /admin/accounts/{provider}/{identifier}/tags [get]
// @Router       /admin/accounts/{provider}/{identifier}/tags [put]
func (s *Server) handleAdminAccountTags(w http.ResponseWriter, r *http.Request) {
	id := s.requireAdminSession(w, r)
	if id == nil {
		return
	}
	provider, identifier, ok := splitAccountTagsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid account path")
		return
	}
	if _, err := s.accounts.Get(provider, identifier); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	key := provider + ":" + identifier
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, EntityTagsResponse{Tags: s.tags.TagsFor(tags.ScopeAccount, key)})
	case http.MethodPut:
		s.adminSetAccountTags(w, r, id, provider, identifier, key)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminSetRepoTags(w http.ResponseWriter, r *http.Request, id *auth.Identity, repo string) {
	var req SetEntityTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := s.tags.SetRepoTags(repo, req.Tags, id.Username)
	if err != nil {
		switch {
		case errors.Is(err, tags.ErrNameRequired), errors.Is(err, tags.ErrNameInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	// Catalogue auto-creations land in the system audit log; per-repo
	// assignment diffs land on this repo's audit ref.
	s.recordTagCreations(id, res.CreatedTags)
	for _, name := range res.Added {
		s.recordRepoAssignmentAudit(repo, "tag.assigned.repo", id, name)
	}
	for _, name := range res.Removed {
		s.recordRepoAssignmentAudit(repo, "tag.unassigned.repo", id, name)
	}
	writeJSON(w, http.StatusOK, EntityTagsResponse{Tags: s.tags.TagsFor(tags.ScopeRepo, repo)})
}

func (s *Server) adminSetAccountTags(w http.ResponseWriter, r *http.Request, id *auth.Identity, provider, identifier, key string) {
	var req SetEntityTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := s.tags.SetAccountTags(key, req.Tags, id.Username)
	if err != nil {
		switch {
		case errors.Is(err, tags.ErrNameRequired), errors.Is(err, tags.ErrNameInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.recordTagCreations(id, res.CreatedTags)
	for _, name := range res.Added {
		s.recordSystemAudit("tag.assigned.account", id, map[string]any{
			"provider":   provider,
			"identifier": identifier,
			"tag":        name,
		})
	}
	for _, name := range res.Removed {
		s.recordSystemAudit("tag.unassigned.account", id, map[string]any{
			"provider":   provider,
			"identifier": identifier,
			"tag":        name,
		})
	}
	writeJSON(w, http.StatusOK, EntityTagsResponse{Tags: s.tags.TagsFor(tags.ScopeAccount, key)})
}

// splitRepoTagsPath pulls {name} out of /api/admin/repos/{name}/tags.
// The repo name is the single path segment between /repos/ and /tags;
// repo names with slashes are not supported (and not produced by
// gigot.repo.go anywhere).
func splitRepoTagsPath(p string) (string, bool) {
	rest := strings.TrimPrefix(p, "/api/admin/repos/")
	if rest == p {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/tags")
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// splitAccountTagsPath pulls {provider}/{identifier} out of
// /api/admin/accounts/{provider}/{identifier}/tags. Same caveat as
// splitAccountsPath about identifiers with slashes — we accept the
// first slash as the provider/identifier boundary and treat the rest
// (minus the trailing /tags) as the identifier.
func splitAccountTagsPath(p string) (provider, identifier string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/admin/accounts/")
	if rest == p {
		return "", "", false
	}
	rest = strings.TrimSuffix(rest, "/tags")
	if rest == "" {
		return "", "", false
	}
	provider, identifier, found := strings.Cut(rest, "/")
	if !found || provider == "" || identifier == "" {
		return "", "", false
	}
	return provider, identifier, true
}

// recordTagCreations writes one tag.created event per auto-created
// catalogue row to the system audit log. Used by every entity-tag
// PUT path: the auto-create-on-assign side effect must also record
// in the audit chain so a future "where did this tag come from?"
// query has the answer.
func (s *Server) recordTagCreations(id *auth.Identity, created []*tags.Tag) {
	for _, t := range created {
		s.recordSystemAudit("tag.created", id, map[string]any{
			"id":   t.ID,
			"name": t.Name,
		})
	}
}

// recordSystemAudit writes one event to the server-wide audit log.
// Failures are swallowed (logged via the underlying writer) so a
// missing audit row doesn't fail an otherwise-successful API call —
// the user-facing operation succeeded, the audit trail just lost a
// row, which is the operator's problem to investigate.
func (s *Server) recordSystemAudit(eventType string, id *auth.Identity, payload map[string]any) {
	if s.systemAudit == nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = s.systemAudit.Append(audit.Event{
		Type:    eventType,
		Actor:   auditActorFromIdentity(id),
		Payload: body,
	})
}

// recordRepoAssignmentAudit appends a tag.assigned.repo /
// tag.unassigned.repo event onto the repo's existing
// refs/audit/main chain. Per design §7.1, repo-bound tag events ride
// the per-repo chain so a `git fetch refs/audit/main` from a mirror
// still tells the whole repo story.
func (s *Server) recordRepoAssignmentAudit(repo, eventType string, id *auth.Identity, tagName string) {
	notesBody, err := json.Marshal(map[string]any{"tag": tagName})
	if err != nil {
		return
	}
	_, _ = s.git.AppendAudit(repo, gitmanager.AuditEvent{
		Type:  eventType,
		Actor: gitmanager.AuditActor{ID: id.ID, Username: id.Username, Provider: id.AccountProvider},
		Notes: string(notesBody),
	})
}
