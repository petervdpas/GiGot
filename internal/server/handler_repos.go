package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/policy"
)

// handleRepos godoc
// @Summary      List or create repositories
// @Description  GET lists all repositories, POST creates a new one
// @Tags         repos
// @Accept       json
// @Produce      json
// @Success      200  {object}  RepoListResponse
// @Success      201  {object}  MessageResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      405  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos [get]
// @Router       /repos [post]
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listRepos(w, r)
	case http.MethodPost:
		s.createRepo(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	if !s.requireAllow(w, r, policy.ActionReadRepo, "") {
		return
	}
	names, err := s.git.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Token-authenticated callers only see their own assigned repos. Admin
	// sessions and the auth-disabled dev identity see everything.
	id := auth.IdentityFromContext(r.Context())
	if id != nil && id.Provider == policy.ProviderToken {
		names = s.filterReposForToken(r, names)
	}

	repos := make([]RepoInfo, 0, len(names))
	for _, name := range names {
		repos = append(repos, RepoInfo{
			Name: name,
			Path: s.git.RepoPath(name),
		})
	}

	writeJSON(w, http.StatusOK, RepoListResponse{
		Repos: repos,
		Count: len(repos),
	})
}

// filterReposForToken narrows a repo-name list to just the entries the token
// on the request is allowed to see. Empty allowlist → empty result.
func (s *Server) filterReposForToken(r *http.Request, names []string) []string {
	entry := s.tokenStrategy.EntryFromRequest(r)
	if entry == nil || len(entry.Repos) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(entry.Repos))
	for _, repo := range entry.Repos {
		allowed[repo] = struct{}{}
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := allowed[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request) {
	if !s.requireAllow(w, r, policy.ActionManageRepos, "") {
		return
	}
	var req CreateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := s.git.InitBare(req.Name); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, MessageResponse{
		Message: "repository " + req.Name + " created",
	})
}

// handleRepo godoc
// @Summary      Get or delete a repository
// @Description  GET returns repository details, DELETE removes it
// @Tags         repos
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {object}  RepoInfo
// @Success      204   "No Content"
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name} [get]
// @Router       /repos/{name} [delete]
func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "repository name is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getRepo(w, r, name)
	case http.MethodDelete:
		s.deleteRepo(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getRepo(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAllow(w, r, policy.ActionReadRepo, name) {
		return
	}
	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
		return
	}

	writeJSON(w, http.StatusOK, RepoInfo{
		Name: name,
		Path: s.git.RepoPath(name),
	})
}

func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAllow(w, r, policy.ActionManageRepos, name) {
		return
	}
	if err := s.git.Delete(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
