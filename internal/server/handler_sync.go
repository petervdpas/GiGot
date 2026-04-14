package server

import (
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
	name := s.extractRepoSubPath(r.URL.Path, "/status")
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return
	}
	if !s.requireAllow(w, r, policy.ActionReadRepo, name) {
		return
	}

	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
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
	name := s.extractRepoSubPath(r.URL.Path, "/branches")
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return
	}
	if !s.requireAllow(w, r, policy.ActionReadRepo, name) {
		return
	}

	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
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
	name := s.extractRepoSubPath(r.URL.Path, "/log")
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return
	}
	if !s.requireAllow(w, r, policy.ActionReadRepo, name) {
		return
	}

	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
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
