package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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
// @Failure      409      {object}  ErrorResponse
// @Failure      422      {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/files/{path} [get]
func (s *Server) handleRepoFile(w http.ResponseWriter, r *http.Request) {
	name, filePath, ok := s.resolveReadableRepoFile(w, r)
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

// writeSyncError maps the sentinel errors returned by sync manager methods
// onto their HTTP responses. Unknown errors fall through as 500 so callers
// don't have to repeat this switch.
func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitmanager.ErrRepoEmpty):
		writeError(w, http.StatusConflict, "repository has no commits yet")
	case errors.Is(err, gitmanager.ErrVersionNotFound):
		writeError(w, http.StatusUnprocessableEntity, "version not found")
	case errors.Is(err, gitmanager.ErrPathNotFound):
		writeError(w, http.StatusNotFound, "path not found at this version")
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
	return s.authorizeReadRepo(w, r, s.extractRepoSubPath(r.URL.Path, suffix))
}

// resolveReadableRepoFile extracts the repo name and sub-path from
// /api/repos/{name}/files/{path} (path may contain slashes) and runs the
// read-policy + existence checks. On any failure writes an error response
// and returns ("", "", false).
func (s *Server) resolveReadableRepoFile(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	name, filePath, found := strings.Cut(trimmed, "/files/")
	if !found || filePath == "" {
		writeError(w, http.StatusBadRequest, "file path is required")
		return "", "", false
	}
	name, ok := s.authorizeReadRepo(w, r, name)
	if !ok {
		return "", "", false
	}
	return name, filePath, true
}

// authorizeReadRepo validates a repo name and runs the read-policy +
// existence checks. On failure writes the error response and returns
// ("", false). Factored out so URL-shape-specific helpers only differ in how
// they parse the name.
func (s *Server) authorizeReadRepo(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return "", false
	}
	if !s.requireAllow(w, r, policy.ActionReadRepo, name) {
		return "", false
	}
	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
		return "", false
	}
	return name, true
}
