package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/petervdpas/GiGot/internal/auth"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
	"github.com/petervdpas/GiGot/internal/tags"
)

// handleRepos godoc
// @Summary      List or create repositories
// @Description  GET lists all repositories, POST creates a new one. Set
// @Description  scaffold_formidable: true to seed the fresh repo with a
// @Description  starter Formidable context (README, templates/basic.yaml,
// @Description  storage/.gitkeep, and the .formidable/context.json marker)
// @Description  in an initial commit. POST appends one `repo_create` entry
// @Description  to refs/audit/main — see docs/design/audit-trail.md.
// @Tags         repos
// @Accept       json
// @Produce      json
// @Param        body  body      CreateRepoRequest  false  "Create-repo body (POST)"
// @Success      200  {object}  RepoListResponse
// @Success      201  {object}  MessageResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      405  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
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
		repos = append(repos, s.repoInfo(name))
	}

	writeJSON(w, http.StatusOK, RepoListResponse{
		Repos: repos,
		Count: len(repos),
	})
}

// repoInfo collects the enriched metadata the admin UI renders on each
// repo card: HEAD SHA + branch, Formidable marker presence, count of
// attached mirror destinations. Empty repos (no commits yet) return
// with Empty=true and head fields unset — callers shouldn't treat that
// as an error, it's a normal state for a fresh bare repo.
func (s *Server) repoInfo(name string) RepoInfo {
	info := RepoInfo{
		Name: name,
		Path: s.git.RepoPath(name),
	}
	if head, err := s.git.Head(name); err == nil {
		info.Head = head.Version
		info.DefaultBranch = head.DefaultBranch
		// Only a non-empty repo can carry a marker — and reading a
		// non-existent blob returns ErrPathNotFound, which is fine.
		if blob, ferr := s.git.File(name, "", formidableMarkerPath); ferr == nil {
			if raw, derr := base64.StdEncoding.DecodeString(blob.ContentB64); derr == nil && isValidFormidableMarker(raw) {
				info.HasFormidable = true
			}
		}
	} else if errors.Is(err, gitmanager.ErrRepoEmpty) {
		info.Empty = true
	}
	if n, err := s.git.CommitCount(name); err == nil {
		info.Commits = n
	}
	info.DestinationCount = len(s.destinations.All(name))
	info.Tags = s.tags.TagsFor(tags.ScopeRepo, name)
	return info
}

// filterReposForToken narrows a repo-name list to just the single
// repo the token is bound to. Subscription keys are one-repo-per-key,
// so this is effectively a membership filter for one string.
func (s *Server) filterReposForToken(r *http.Request, names []string) []string {
	entry := s.tokenStrategy.EntryFromRequest(r)
	if entry == nil || entry.Repo == "" {
		return nil
	}
	for _, n := range names {
		if n == entry.Repo {
			return []string{n}
		}
	}
	return nil
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

	// Decision matrix per docs/design/structured-sync-api.md §2.7.1: the
	// per-request flag wins when explicit, otherwise fall back to the
	// server-level default. Everything else downstream branches on
	// (isClone, stamp).
	stamp := resolveShouldStamp(s.cfg.Server.FormidableFirst, req.ScaffoldFormidable)
	isClone := req.SourceURL != ""

	if isClone {
		if err := s.git.CloneBare(req.Name, req.SourceURL); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// Back-fill refs/audit/main with the upstream commit history
		// BEFORE the optional stamp step so only user-authored upstream
		// commits appear in the back-fill. Any marker-stamp commit that
		// follows is server-internal (like scaffold) and deliberately
		// not represented in the audit chain — the repo_create entry's
		// notes already say "stamped as Formidable context".
		if _, err := s.git.SeedAuditFromHistory(req.Name); err != nil {
			log.Printf("audit: backfill on repo %q failed: %v", req.Name, err)
		}
	} else {
		if err := s.git.InitBare(req.Name); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}

	stampedOnClone := false
	switch {
	case !stamp:
		// Nothing to do — repo stays as-is.
	case isClone:
		// A cloned repo almost never carries the full Formidable layout,
		// so stamping just the marker leaves `templates/` and `storage/`
		// missing — the repo looks like Formidable but isn't usable.
		// ensureFormidableShape adds only what's missing and leaves
		// existing content alone (never overwrites README or any file
		// already at a scaffold path).
		added, err := ensureFormidableShape(s.git, req.Name, time.Now())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stamping marker: "+err.Error())
			return
		}
		stampedOnClone = len(added) > 0
	default:
		files, err := formidableScaffoldFiles(time.Now())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scaffold payload: "+err.Error())
			return
		}
		if err := s.git.Scaffold(req.Name, gitmanager.ScaffoldOptions{
			CommitterName:  scaffoldCommitterName,
			CommitterEmail: scaffoldCommitterEmail,
			Message:        scaffoldCommitMessage,
			Files:          files,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "scaffolding failed: "+err.Error())
			return
		}
	}

	msg := "repository " + req.Name
	switch {
	case isClone && stampedOnClone:
		msg += " cloned from " + req.SourceURL + " and stamped as Formidable context"
	case isClone && stamp:
		msg += " cloned from " + req.SourceURL + " (existing Formidable marker preserved)"
	case isClone:
		msg += " cloned from " + req.SourceURL
	case stamp:
		msg += " created (scaffolded as Formidable context)"
	default:
		msg += " created"
	}
	s.appendAudit(req.Name, gitmanager.AuditEvent{
		Type:  AuditTypeRepoCreate,
		Actor: auditActor(r),
		Notes: msg,
	})
	writeJSON(w, http.StatusCreated, MessageResponse{Message: msg})
}

// resolveShouldStamp implements the §2.7.1 tri-state resolution:
// explicit request flag wins; otherwise fall back to the server-level
// default. Pure function so it can be exhaustively table-tested.
func resolveShouldStamp(serverDefault bool, requested *bool) bool {
	if requested != nil {
		return *requested
	}
	return serverDefault
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

	writeJSON(w, http.StatusOK, s.repoInfo(name))
}

func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAllow(w, r, policy.ActionManageRepos, name) {
		return
	}
	if err := s.git.Delete(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Drop any destinations scoped to this repo so they can't dangle
	// under a name that no longer exists.
	if err := s.destinations.RemoveAll(name); err != nil {
		// Repo is gone; surface the cleanup failure as 500 so the
		// admin knows the destinations file needs manual attention.
		writeError(w, http.StatusInternalServerError, "repo deleted but destinations cleanup failed: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
