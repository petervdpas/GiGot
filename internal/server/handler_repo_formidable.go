package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
	"github.com/petervdpas/GiGot/internal/scaffold"
)

// RepoFormidableResponse is the bootstrap payload for the
// Formidable-flavoured part of a repo. Pairs with /context: where
// /context says whether this is a Formidable repo, /formidable says
// what its Formidable shape is — scaffold marker, available
// templates, storage layout. One read on connect; the client
// renders sidebar + template picker + scaffold-version checks off
// this response.
//
// Non-Formidable repos still return 200 with MarkerPresent=false
// and empty Templates/Storage so a client can distinguish "not a
// Formidable repo" from "out of scope" or "doesn't exist."
type RepoFormidableResponse struct {
	MarkerPresent bool                  `json:"marker_present"`
	Marker        *FormidableMarkerView `json:"marker,omitempty"`
	Templates     []FormidableTemplate  `json:"templates"`
	Storage       []FormidableStorage   `json:"storage"`
}

// FormidableMarkerView mirrors the .formidable/context.json payload
// the scaffolder writes. Surfaced so a client can detect scaffold
// version mismatches and show "this repo was scaffolded by an older
// GiGot — upgrade?" without parsing the marker itself.
type FormidableMarkerView struct {
	Version      int    `json:"version"`
	ScaffoldedBy string `json:"scaffolded_by,omitempty"`
	ScaffoldedAt string `json:"scaffolded_at,omitempty"`
}

// FormidableTemplate is one entry under templates/ at HEAD. Path is
// repo-relative (e.g. "templates/basic.yaml") so the client can
// fetch it via the existing /files endpoint without re-deriving the
// path. Name is the bare filename minus extension for sidebar use.
type FormidableTemplate struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FormidableStorage is one template-directory under storage/ that
// holds at least one file. Lets a client populate the sidebar with
// directory names that actually have content without listing every
// blob.
type FormidableStorage struct {
	Template string `json:"template"`
	Files    int    `json:"files"`
}

// handleRepoFormidable godoc
// @Summary      Formidable-shape bootstrap for a repo
// @Description  One-call read of the Formidable-specific shape of a
// @Description  repo: scaffold marker (version/by/at), templates list
// @Description  under templates/, and storage layout under storage/.
// @Description  Non-Formidable repos return 200 with marker_present
// @Description  false and empty arrays so clients can distinguish
// @Description  "not Formidable" from "doesn't exist" or "no access."
// @Tags         repos
// @Produce      json
// @Param        name  path      string  true  "Repository name"
// @Success      200   {object}  RepoFormidableResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/formidable [get]
func (s *Server) handleRepoFormidable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	repo, ok := s.resolveFormidableRepo(w, r)
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

	out := RepoFormidableResponse{
		Templates: []FormidableTemplate{},
		Storage:   []FormidableStorage{},
	}

	// Reading the marker / tree on an empty repo isn't an error —
	// it's the "freshly created, no commits" state. Return the
	// zero-value response in that case.
	head, err := s.git.Head(repo)
	if err != nil {
		if errors.Is(err, gitmanager.ErrRepoEmpty) {
			writeJSON(w, http.StatusOK, out)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if blob, ferr := s.git.File(repo, "", scaffold.MarkerPath); ferr == nil {
		if raw, derr := base64.StdEncoding.DecodeString(blob.ContentB64); derr == nil && scaffold.IsValidMarker(raw) {
			out.MarkerPresent = true
			out.Marker = parseMarkerView(raw)
		}
	}

	tree, err := s.git.Tree(repo, head.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Templates = collectFormidableTemplates(tree.Files)
	out.Storage = collectFormidableStorage(tree.Files)

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) resolveFormidableRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	if rest == r.URL.Path {
		writeError(w, http.StatusBadRequest, "invalid formidable path")
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "formidable" {
		writeError(w, http.StatusBadRequest, "invalid formidable path")
		return "", false
	}
	return parts[0], true
}

func parseMarkerView(raw []byte) *FormidableMarkerView {
	var m struct {
		Version      int    `json:"version"`
		ScaffoldedBy string `json:"scaffolded_by"`
		ScaffoldedAt string `json:"scaffolded_at"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &FormidableMarkerView{
		Version:      m.Version,
		ScaffoldedBy: m.ScaffoldedBy,
		ScaffoldedAt: m.ScaffoldedAt,
	}
}

// collectFormidableTemplates picks up "templates/<name>.yaml" entries
// at the top level of the templates/ directory. Sub-directories are
// ignored — Formidable templates are flat YAML files by convention.
func collectFormidableTemplates(tree []gitmanager.TreeEntry) []FormidableTemplate {
	const prefix = "templates/"
	var out []FormidableTemplate
	for _, e := range tree {
		if !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		rest := e.Path[len(prefix):]
		if strings.Contains(rest, "/") {
			continue
		}
		if !strings.HasSuffix(rest, ".yaml") {
			continue
		}
		name := strings.TrimSuffix(rest, ".yaml")
		out = append(out, FormidableTemplate{Name: name, Path: e.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// collectFormidableStorage groups storage entries by their first
// segment (the template-directory name) and counts files under each.
// Entries directly at storage/ root are ignored — Formidable's
// storage layout is always storage/<template>/...
func collectFormidableStorage(tree []gitmanager.TreeEntry) []FormidableStorage {
	const prefix = "storage/"
	counts := make(map[string]int)
	for _, e := range tree {
		if !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		rest := e.Path[len(prefix):]
		// Need at least <template>/<file>.
		idx := strings.Index(rest, "/")
		if idx <= 0 {
			continue
		}
		template := rest[:idx]
		counts[template]++
	}
	out := make([]FormidableStorage, 0, len(counts))
	for tpl, n := range counts {
		out = append(out, FormidableStorage{Template: tpl, Files: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Template < out[j].Template })
	return out
}
