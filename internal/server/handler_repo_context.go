package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
)

// RepoContextResponse is the bootstrap payload an API client (e.g. a
// Formidable instance connecting via subscription key) reads on
// connect to know what UI to render. One call answers three
// questions: who am I, what can I do here, and what does this repo
// offer. Without this endpoint a client has to stitch together
// /api/me + /api/repos/{name} + /api/repos/{name}/destinations and
// infer the rest, which is exactly the coupling we want to retire.
type RepoContextResponse struct {
	User         RepoContextUser         `json:"user"`
	Subscription RepoContextSubscription `json:"subscription"`
	Repo         RepoContextRepo         `json:"repo"`
}

// RepoContextUser is the caller's account profile (resolved from
// session cookie or bearer-token Username). Role is always populated
// — falls back to "regular" if the underlying account row is gone.
type RepoContextUser struct {
	Username    string `json:"username"`
	Provider    string `json:"provider,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Role        string `json:"role"`
}

// RepoContextSubscription describes the bearer's subscription as the
// server sees it. Empty when the caller is session-authed (admins
// driving the same endpoint from a browser); populated when the
// caller is a bearer client. Repo is echo'd so the client can verify
// it's looking at the right repo binding.
type RepoContextSubscription struct {
	Repo      string   `json:"repo,omitempty"`
	Abilities []string `json:"abilities"`
}

// RepoContextRepo is the repo's capability snapshot. Empty repos
// surface with Empty=true and the head fields cleared — that's a
// normal first-commit state, not an error. Destinations is a count
// summary so a client can decide whether to render the Mirror
// section without making a second call.
type RepoContextRepo struct {
	Name          string                  `json:"name"`
	Empty         bool                    `json:"empty"`
	HeadSha       string                  `json:"head_sha,omitempty"`
	DefaultBranch string                  `json:"default_branch,omitempty"`
	Commits       int                     `json:"commits"`
	IsFormidable  bool                    `json:"is_formidable"`
	Destinations  RepoContextDestinations `json:"destinations"`
}

// RepoContextDestinations counts what's attached to the repo,
// without leaking URLs/credentials a non-mirror reader shouldn't
// learn. AutoMirrorEnabled is the subset of Total whose Enabled
// flag is true — the server fans out a push to those on every
// accepted commit. Total - AutoMirrorEnabled is the manual-only
// subset; clients can compute that locally.
type RepoContextDestinations struct {
	Total             int `json:"total"`
	AutoMirrorEnabled int `json:"auto_mirror_enabled"`
}

// handleRepoContext godoc
// @Summary      Bootstrap context for a repo connection
// @Description  Single-call answer to "who am I, what can I do here,
// @Description  what does this repo offer." Designed for API clients
// @Description  (Formidable etc.) that need to render permission-aware
// @Description  UI without probing per-feature endpoints. Read-only;
// @Description  requires only repo-scope read access — the caller's
// @Description  abilities are reported, not gated.
// @Tags        repos
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {object}  RepoContextResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/context [get]
func (s *Server) handleRepoContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	repo, ok := s.resolveContextRepo(w, r)
	if !ok {
		return
	}
	if !s.requireAllow(w, r, policy.ActionReadRepo, repo) {
		return
	}
	if !s.git.Exists(repo) {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	resp := RepoContextResponse{
		Repo:         s.buildRepoContextRepo(repo),
		User:         s.buildRepoContextUser(r),
		Subscription: s.buildRepoContextSubscription(r),
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveContextRepo extracts the {name} segment from
// /api/repos/{name}/context. Sibling of resolveReadableRepo but
// scoped to this one route so future repo subroutes don't have to
// agree on a path-parsing helper.
func (s *Server) resolveContextRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	if rest == r.URL.Path {
		writeError(w, http.StatusBadRequest, "invalid repo context path")
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "context" {
		writeError(w, http.StatusBadRequest, "invalid repo context path")
		return "", false
	}
	return parts[0], true
}

func (s *Server) buildRepoContextUser(r *http.Request) RepoContextUser {
	id := auth.IdentityFromContext(r.Context())
	out := RepoContextUser{Role: accounts.RoleRegular}
	if id == nil {
		return out
	}
	out.Username = id.Username
	out.Provider = id.AccountProvider
	prov, ident := id.AccountProvider, id.Username
	// Bearer identities don't carry AccountProvider — parse it out
	// of the scoped Username so we look up the right account.
	if prov == "" {
		if p, i, err := parseTokenUsername(id.Username); err == nil {
			prov, ident = p, i
			out.Provider = p
		}
	}
	if prov != "" {
		if acc, err := s.accounts.Get(prov, ident); err == nil {
			out.DisplayName = acc.DisplayName
			out.Email = acc.Email
			if acc.Role != "" {
				out.Role = acc.Role
			}
		}
	}
	return out
}

func (s *Server) buildRepoContextSubscription(r *http.Request) RepoContextSubscription {
	out := RepoContextSubscription{Abilities: []string{}}
	entry := s.tokenStrategy.EntryFromRequest(r)
	if entry == nil {
		return out
	}
	out.Repo = entry.Repo
	if entry.Abilities != nil {
		out.Abilities = entry.Abilities
	}
	return out
}

func (s *Server) buildRepoContextRepo(name string) RepoContextRepo {
	out := RepoContextRepo{Name: name}
	if head, err := s.git.Head(name); err == nil {
		out.HeadSha = head.Version
		out.DefaultBranch = head.DefaultBranch
		// Marker check is only meaningful on a non-empty repo —
		// File() on an empty repo returns ErrRepoEmpty, not a "no
		// such file" we'd want to ignore.
		if blob, ferr := s.git.File(name, "", formidableMarkerPath); ferr == nil {
			if raw, derr := base64.StdEncoding.DecodeString(blob.ContentB64); derr == nil && isValidFormidableMarker(raw) {
				out.IsFormidable = true
			}
		}
	} else if errors.Is(err, gitmanager.ErrRepoEmpty) {
		out.Empty = true
	}
	if n, err := s.git.CommitCount(name); err == nil {
		out.Commits = n
	}
	dests := s.destinations.All(name)
	out.Destinations.Total = len(dests)
	for _, d := range dests {
		if d.Enabled {
			out.Destinations.AutoMirrorEnabled++
		}
	}
	return out
}
