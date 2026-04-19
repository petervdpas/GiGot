package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/formidable"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
)

// RepoStatusResponse describes the current state of a repository.
type RepoStatusResponse struct {
	Name     string                  `json:"name" example:"my-templates"`
	Branches []gitmanager.BranchInfo `json:"branches"`
	Empty    bool                    `json:"empty" example:"false"`
}

// RepoLogResponse contains recent commits.
type RepoLogResponse struct {
	Name    string                `json:"name" example:"my-templates"`
	Entries []gitmanager.LogEntry `json:"entries"`
	Count   int                   `json:"count" example:"5"`
}

// handleRepoHead godoc
// @Summary      Repository HEAD pointer
// @Description  Returns the current commit SHA and default branch name. Clients
// @Description  use this as a cheap probe before pulling tree or snapshot. Returns
// @Description  409 if the repo exists but has no commits yet.
// @Tags         sync
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {object}  git.HeadInfo
// @Failure      404   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/head [get]
func (s *Server) handleRepoHead(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/head")
	if !ok {
		return
	}

	info, err := s.git.Head(name)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRepoTree godoc
// @Summary      Repository tree listing
// @Description  Returns the recursive blob listing at the given version (or
// @Description  HEAD when omitted). Clients diff this against their local
// @Description  snapshot before pulling content. Returns 409 if the repo has
// @Description  no commits yet, 422 if version does not resolve.
// @Tags         sync
// @Produce      json
// @Param        name     path      string  true   "Repository name"
// @Param        version  query     string  false  "Commit SHA (defaults to HEAD)"
// @Success      200      {object}  git.TreeInfo
// @Failure      404      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Failure      422      {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/tree [get]
func (s *Server) handleRepoTree(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/tree")
	if !ok {
		return
	}

	info, err := s.git.Tree(name, r.URL.Query().Get("version"))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRepoSnapshot godoc
// @Summary      Repository snapshot
// @Description  Returns every blob at the given version with content
// @Description  base64-encoded. Intended for initial client populate and
// @Description  disaster recovery; prefer /tree + /files/{path} for
// @Description  incremental syncing.
// @Tags         sync
// @Produce      json
// @Param        name     path      string  true   "Repository name"
// @Param        version  query     string  false  "Commit SHA (defaults to HEAD)"
// @Success      200      {object}  git.SnapshotInfo
// @Failure      404      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Failure      422      {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/snapshot [get]
func (s *Server) handleRepoSnapshot(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/snapshot")
	if !ok {
		return
	}

	info, err := s.git.Snapshot(name, r.URL.Query().Get("version"))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRepoChanges godoc
// @Summary      Delta since version
// @Description  Returns the set of paths added, modified, or deleted between
// @Description  the given since version and current HEAD. since must be a
// @Description  strict ancestor of HEAD (or equal to it); anything else
// @Description  returns 409 so the client can re-snapshot instead of
// @Description  consuming a misleading diff. since == HEAD returns an empty
// @Description  changes list.
// @Tags         sync
// @Produce      json
// @Param        name   path      string  true   "Repository name"
// @Param        since  query     string  true   "Client's last-seen commit SHA"
// @Success      200    {object}  git.ChangesInfo
// @Failure      400    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      405    {object}  ErrorResponse
// @Failure      409    {object}  ErrorResponse
// @Failure      422    {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/changes [get]
func (s *Server) handleRepoChanges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, ok := s.resolveReadableRepo(w, r, "/changes")
	if !ok {
		return
	}
	since := r.URL.Query().Get("since")
	if since == "" {
		writeError(w, http.StatusBadRequest, "since is required")
		return
	}

	info, err := s.git.Changes(name, since)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRepoFile godoc
// @Summary      Single-file read
// @Description  Returns one file's content at the given version (default HEAD),
// @Description  base64-encoded. 404 covers both missing repo and path not in
// @Description  version; 422 covers an unresolvable version.
// @Tags         sync
// @Produce      json
// @Param        name     path      string  true   "Repository name"
// @Param        path     path      string  true   "File path inside the repo"
// @Param        version  query     string  false  "Commit SHA (defaults to HEAD)"
// @Success      200      {object}  git.FileInfo
// @Failure      404      {object}  ErrorResponse
// @Failure      405      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Failure      422      {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/files/{path} [get]
func (s *Server) handleRepoFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleRepoFileGet(w, r)
	case http.MethodPut:
		s.handleRepoFilePut(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRepoFileGet(w http.ResponseWriter, r *http.Request) {
	name, filePath, ok := s.resolveRepoFile(w, r, policy.ActionReadRepo)
	if !ok {
		return
	}

	info, err := s.git.File(name, r.URL.Query().Get("version"), filePath)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// WriteFileRequest is the body of PUT /repos/{name}/files/{path}. ParentVersion
// is the commit SHA the client last saw; ContentB64 is the new file body.
// Author is optional — when omitted, the server stamps the subscription-key
// username as author. Committer is always the server's scaffolder identity.
type WriteFileRequest struct {
	ParentVersion string      `json:"parent_version" example:"abc123..."`
	ContentB64    string      `json:"content_b64" example:"aGVsbG8K"`
	Author        *AuthorInfo `json:"author,omitempty"`
	Message       string      `json:"message,omitempty" example:"Update basic template"`
}

// AuthorInfo is the optional author block on a write request.
type AuthorInfo struct {
	Name  string `json:"name" example:"Alice"`
	Email string `json:"email" example:"alice@example.com"`
}

// WriteFileConflictResponse is the 409 body when a write cannot be merged.
// Matches docs/design/structured-sync-api.md §3.5 — base/theirs may be empty
// for add/add or delete/modify shapes, and are both empty on a stale-parent
// 409 where the server did not attempt a merge.
type WriteFileConflictResponse struct {
	CurrentVersion string `json:"current_version" example:"def456..."`
	Path           string `json:"path" example:"templates/basic.yaml"`
	BaseB64        string `json:"base_b64,omitempty"`
	TheirsB64      string `json:"theirs_b64,omitempty"`
	YoursB64       string `json:"yours_b64"`
}

// handleRepoFilePut godoc
// @Summary      Single-file write
// @Description  Commits one file against the given parent_version. Returns
// @Description  200 with the new version for a fast-forward or auto-merged
// @Description  commit (merged_from/merged_with populated on auto-merge).
// @Description  Returns 409 with base/theirs/yours blobs on a real conflict,
// @Description  or 409 with only current_version + yours when parent_version
// @Description  is not an ancestor of HEAD.
// @Tags         sync
// @Accept       json
// @Produce      json
// @Param        name  path      string              true  "Repository name"
// @Param        path  path      string              true  "File path inside the repo"
// @Param        body  body      WriteFileRequest    true  "Write request"
// @Success      200   {object}  git.WriteResult
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  WriteFileConflictResponse
// @Failure      409   {object}  formidable.RecordConflict  "Structured record merge conflict (immutable meta field)"
// @Failure      422   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/files/{path} [put]
func (s *Server) handleRepoFilePut(w http.ResponseWriter, r *http.Request) {
	name, filePath, ok := s.resolveRepoFile(w, r, policy.ActionWriteRepo)
	if !ok {
		return
	}

	var req WriteFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ParentVersion == "" {
		writeError(w, http.StatusBadRequest, "parent_version is required")
		return
	}
	content, err := base64.StdEncoding.DecodeString(req.ContentB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "content_b64 is not valid base64")
		return
	}

	authorName, authorEmail := s.resolveAuthor(r, req.Author)
	if authorName == "" || authorEmail == "" {
		writeError(w, http.StatusBadRequest, "author identity could not be determined")
		return
	}

	subUsername := ""
	if id := auth.IdentityFromContext(r.Context()); id != nil {
		subUsername = id.Username
	}

	// Structured Formidable merge: if this write targets a record file in
	// a marker-stamped repo and the parent is stale, pre-merge per-field
	// (§10.2–§10.3) and fast-forward the canonical bytes on top of HEAD.
	// A nil merged + nil conflict + applicable=false means this write is
	// not a Formidable candidate; fall through to the generic path.
	originalParent := req.ParentVersion
	mergedBlob, recConflict, headVersion, applicable, mergeErr := s.maybeFormidableMerge(name, filePath, req.ParentVersion, content)
	if mergeErr != nil {
		writeError(w, http.StatusInternalServerError, mergeErr.Error())
		return
	}
	if recConflict != nil {
		writeJSON(w, http.StatusConflict, recConflict)
		return
	}
	formidableMerged := false
	if applicable && mergedBlob != nil {
		content = mergedBlob
		req.ParentVersion = headVersion
		formidableMerged = true
	}

	res, err := s.git.WriteFile(name, gitmanager.WriteOptions{
		ParentVersion:        req.ParentVersion,
		Path:                 filePath,
		Content:              content,
		AuthorName:           authorName,
		AuthorEmail:          authorEmail,
		CommitterName:        scaffoldCommitterName,
		CommitterEmail:       scaffoldCommitterEmail,
		Message:              req.Message,
		SubscriptionUsername: subUsername,
	})
	if err != nil {
		var ce *gitmanager.WriteConflictError
		if errors.As(err, &ce) {
			writeJSON(w, http.StatusConflict, WriteFileConflictResponse{
				CurrentVersion: ce.Conflict.CurrentVersion,
				Path:           ce.Conflict.Path,
				BaseB64:        ce.Conflict.BaseB64,
				TheirsB64:      ce.Conflict.TheirsB64,
				YoursB64:       ce.Conflict.YoursB64,
			})
			return
		}
		writeSyncError(w, err)
		return
	}
	// A Formidable merge always fast-forwards at the git level, but
	// semantically it was a merge — surface MergedFrom/MergedWith so the
	// client sees the same "this was a merge" signal as the generic path.
	if formidableMerged {
		res.FastForward = false
		res.MergedFrom = originalParent
		res.MergedWith = headVersion
	}
	s.appendAudit(name, gitmanager.AuditEvent{
		Type:  AuditTypeFilePut,
		Actor: auditActor(r),
		SHA:   res.Version,
		Notes: filePath,
	})
	writeJSON(w, http.StatusOK, res)
}

// ChangeRequest is one entry in the changes[] array of POST /commits.
// Op is "put" or "delete"; ContentB64 is required for put, ignored for delete.
type ChangeRequest struct {
	Op         string `json:"op" example:"put"`
	Path       string `json:"path" example:"templates/basic.yaml"`
	ContentB64 string `json:"content_b64,omitempty" example:"aGVsbG8K"`
}

// CommitRequest is the body of POST /repos/{name}/commits. See
// docs/design/structured-sync-api.md §3.6. All changes apply atomically:
// any per-path conflict aborts the whole commit.
type CommitRequest struct {
	ParentVersion string          `json:"parent_version" example:"abc123..."`
	Changes       []ChangeRequest `json:"changes"`
	Author        *AuthorInfo     `json:"author,omitempty"`
	Message       string          `json:"message,omitempty" example:"Rename a→c"`
}

// CommitConflictResponse is the 409 body when a commit cannot be merged.
// conflicts[] mirrors the single-path shape of WriteFileConflictResponse
// for each failing path. On a stale-parent 409 every change is echoed back
// with only yours_b64 populated.
type CommitConflictResponse struct {
	CurrentVersion string                      `json:"current_version" example:"def456..."`
	Conflicts      []WriteFileConflictResponse `json:"conflicts"`
}

// CommitRecordConflictResponse is the 409 body when one or more
// Formidable records in a commit violate immutable meta fields
// (§10.6). Transactional: any record conflict aborts the whole commit
// before the generic merge runs. Structurally distinct from
// CommitConflictResponse so clients can switch on the body shape.
type CommitRecordConflictResponse struct {
	CurrentVersion string                        `json:"current_version" example:"def456..."`
	RecordConflicts []formidable.RecordConflict  `json:"record_conflicts"`
}

// handleRepoCommits godoc
// @Summary      Multi-file atomic commit
// @Description  Applies an ordered list of put/delete changes against the
// @Description  given parent_version as a single commit. Returns 200 with
// @Description  the new version on success (fast-forward or auto-merged).
// @Description  Returns 409 with conflicts[] if any path conflicts — the
// @Description  whole commit is rejected, no partial apply.
// @Tags         sync
// @Accept       json
// @Produce      json
// @Param        name  path      string         true  "Repository name"
// @Param        body  body      CommitRequest  true  "Commit request"
// @Success      200   {object}  git.CommitResult
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  CommitConflictResponse
// @Failure      409   {object}  CommitRecordConflictResponse  "One or more Formidable records violated immutable meta fields"
// @Failure      422   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/commits [post]
func (s *Server) handleRepoCommits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, ok := s.resolveReadableRepoWithAction(w, r, "/commits", policy.ActionWriteRepo)
	if !ok {
		return
	}

	var req CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ParentVersion == "" {
		writeError(w, http.StatusBadRequest, "parent_version is required")
		return
	}
	if len(req.Changes) == 0 {
		writeError(w, http.StatusBadRequest, "changes is required")
		return
	}

	changes := make([]gitmanager.Change, 0, len(req.Changes))
	for i, c := range req.Changes {
		if c.Op != gitmanager.OpPut && c.Op != gitmanager.OpDelete {
			writeError(w, http.StatusBadRequest,
				"changes["+strconv.Itoa(i)+"]: op must be \"put\" or \"delete\"")
			return
		}
		if c.Path == "" {
			writeError(w, http.StatusBadRequest,
				"changes["+strconv.Itoa(i)+"]: path is required")
			return
		}
		mc := gitmanager.Change{Op: c.Op, Path: c.Path}
		if c.Op == gitmanager.OpPut {
			content, err := base64.StdEncoding.DecodeString(c.ContentB64)
			if err != nil {
				writeError(w, http.StatusBadRequest,
					"changes["+strconv.Itoa(i)+"]: content_b64 is not valid base64")
				return
			}
			mc.Content = content
		}
		changes = append(changes, mc)
	}

	authorName, authorEmail := s.resolveAuthor(r, req.Author)
	if authorName == "" || authorEmail == "" {
		writeError(w, http.StatusBadRequest, "author identity could not be determined")
		return
	}

	subUsername := ""
	if id := auth.IdentityFromContext(r.Context()); id != nil {
		subUsername = id.Username
	}

	// Pre-scan record-path puts for immutable-meta violations. F1
	// scope: reject the whole commit on any record-level conflict; do
	// not attempt auto-merge-on-commit (deferred). Non-record changes
	// and non-record repos fall through unaffected.
	var recordConflicts []formidable.RecordConflict
	var recordHeadVersion string
	for _, c := range changes {
		if c.Op != gitmanager.OpPut {
			continue
		}
		if !isFormidableRecordPath(c.Path) {
			continue
		}
		_, rc, headV, applicable, mergeErr := s.maybeFormidableMerge(name, c.Path, req.ParentVersion, c.Content)
		if mergeErr != nil {
			writeError(w, http.StatusInternalServerError, mergeErr.Error())
			return
		}
		if applicable && rc != nil {
			recordConflicts = append(recordConflicts, *rc)
			recordHeadVersion = headV
		}
	}
	if len(recordConflicts) > 0 {
		writeJSON(w, http.StatusConflict, CommitRecordConflictResponse{
			CurrentVersion:  recordHeadVersion,
			RecordConflicts: recordConflicts,
		})
		return
	}

	res, err := s.git.Commit(name, gitmanager.CommitOptions{
		ParentVersion:        req.ParentVersion,
		Changes:              changes,
		AuthorName:           authorName,
		AuthorEmail:          authorEmail,
		CommitterName:        scaffoldCommitterName,
		CommitterEmail:       scaffoldCommitterEmail,
		Message:              req.Message,
		SubscriptionUsername: subUsername,
	})
	if err != nil {
		var ce *gitmanager.CommitConflictError
		if errors.As(err, &ce) {
			conflicts := make([]WriteFileConflictResponse, 0, len(ce.Conflicts))
			for _, c := range ce.Conflicts {
				conflicts = append(conflicts, WriteFileConflictResponse{
					CurrentVersion: c.CurrentVersion,
					Path:           c.Path,
					BaseB64:        c.BaseB64,
					TheirsB64:      c.TheirsB64,
					YoursB64:       c.YoursB64,
				})
			}
			writeJSON(w, http.StatusConflict, CommitConflictResponse{
				CurrentVersion: ce.CurrentVersion,
				Conflicts:      conflicts,
			})
			return
		}
		writeSyncError(w, err)
		return
	}
	s.appendAudit(name, gitmanager.AuditEvent{
		Type:  AuditTypeCommit,
		Actor: auditActor(r),
		SHA:   res.Version,
	})
	writeJSON(w, http.StatusOK, res)
}

// resolveReadableRepoWithAction is the generalised form of
// resolveReadableRepo that takes an explicit action (so POST /commits can
// require ActionWriteRepo rather than ActionReadRepo). Kept as a thin
// wrapper around authorizeRepo so URL-suffix parsing stays in one place.
func (s *Server) resolveReadableRepoWithAction(w http.ResponseWriter, r *http.Request, suffix string, action policy.Action) (string, bool) {
	return s.authorizeRepo(w, r, s.extractRepoSubPath(r.URL.Path, suffix), action)
}

// resolveAuthor picks the author identity for a write: client-supplied
// values win when present; otherwise fall back to "<username>@gigot.local"
// derived from the authenticated identity. Returns empty strings when no
// identity is attached — the caller rejects that as a 400.
func (s *Server) resolveAuthor(r *http.Request, supplied *AuthorInfo) (string, string) {
	if supplied != nil && supplied.Name != "" && supplied.Email != "" {
		return supplied.Name, supplied.Email
	}
	id := auth.IdentityFromContext(r.Context())
	if id == nil || id.Username == "" {
		return "", ""
	}
	return id.Username, id.Username + "@gigot.local"
}

// writeSyncError maps the sentinel errors returned by sync manager methods
// onto their HTTP responses. Unknown errors fall through as 500 so callers
// don't have to repeat this switch.
func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitmanager.ErrRepoEmpty):
		writeError(w, http.StatusConflict, "repository has no commits yet")
	case errors.Is(err, gitmanager.ErrStaleParent):
		writeError(w, http.StatusConflict, "since is not an ancestor of current head")
	case errors.Is(err, gitmanager.ErrVersionNotFound):
		writeError(w, http.StatusUnprocessableEntity, "version not found")
	case errors.Is(err, gitmanager.ErrPathNotFound):
		writeError(w, http.StatusNotFound, "path not found at this version")
	case errors.Is(err, gitmanager.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "path is not valid inside the repository")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleRepoStatus godoc
// @Summary      Repository status
// @Description  Returns branches and status of a repository
// @Tags         sync
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {object}  RepoStatusResponse
// @Failure      404   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/status [get]
func (s *Server) handleRepoStatus(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/status")
	if !ok {
		return
	}

	branches, _ := s.git.Branches(name)

	writeJSON(w, http.StatusOK, RepoStatusResponse{
		Name:     name,
		Branches: branches,
		Empty:    len(branches) == 0,
	})
}

// handleRepoBranches godoc
// @Summary      List branches
// @Description  Returns all branches in a repository
// @Tags         sync
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {array}   gitmanager.BranchInfo
// @Failure      404   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/branches [get]
func (s *Server) handleRepoBranches(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/branches")
	if !ok {
		return
	}

	branches, err := s.git.Branches(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, branches)
}

// handleRepoLog godoc
// @Summary      Commit log
// @Description  Returns recent commits from a repository
// @Tags         sync
// @Produce      json
// @Param        name   path      string  true   "Repository name"
// @Param        limit  query     int     false  "Max number of commits"  default(20)
// @Success      200    {object}  RepoLogResponse
// @Failure      404    {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/log [get]
func (s *Server) handleRepoLog(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveReadableRepo(w, r, "/log")
	if !ok {
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	entries, err := s.git.Log(name, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, RepoLogResponse{
		Name:    name,
		Entries: entries,
		Count:   len(entries),
	})
}

// extractRepoSubPath extracts the repo name from paths like /api/repos/{name}/{suffix}.
func (s *Server) extractRepoSubPath(path, suffix string) string {
	trimmed := strings.TrimPrefix(path, "/api/repos/")
	trimmed = strings.TrimSuffix(trimmed, suffix)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

// resolveReadableRepo extracts the repo name from /api/repos/{name}/{suffix}
// and runs the read-policy + existence checks. Returns (name, true) on
// success; on failure writes the appropriate error response and returns
// ("", false).
func (s *Server) resolveReadableRepo(w http.ResponseWriter, r *http.Request, suffix string) (string, bool) {
	return s.authorizeRepo(w, r, s.extractRepoSubPath(r.URL.Path, suffix), policy.ActionReadRepo)
}

// resolveRepoFile extracts the repo name and sub-path from
// /api/repos/{name}/files/{path} (path may contain slashes) and runs the
// policy + existence checks for the given action. On any failure writes an
// error response and returns ("", "", false).
func (s *Server) resolveRepoFile(w http.ResponseWriter, r *http.Request, action policy.Action) (string, string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	name, filePath, found := strings.Cut(trimmed, "/files/")
	if !found || filePath == "" {
		writeError(w, http.StatusBadRequest, "file path is required")
		return "", "", false
	}
	name, ok := s.authorizeRepo(w, r, name, action)
	if !ok {
		return "", "", false
	}
	return name, filePath, true
}

// authorizeRepo validates a repo name and runs the policy + existence checks
// for the given action. On failure writes the error response and returns
// ("", false). Factored out so URL-shape-specific helpers only differ in how
// they parse the name.
func (s *Server) authorizeRepo(w http.ResponseWriter, r *http.Request, name string, action policy.Action) (string, bool) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return "", false
	}
	if !s.requireAllow(w, r, action, name) {
		return "", false
	}
	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
		return "", false
	}
	return name, true
}
