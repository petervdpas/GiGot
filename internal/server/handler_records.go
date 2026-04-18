package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/petervdpas/GiGot/internal/formidable"
	"github.com/petervdpas/GiGot/internal/policy"
)

// RecordQueryResponse is the 200 body for GET /records/{template}.
// Version is the HEAD SHA the query was evaluated against; Records
// holds the filtered+sorted+limited slice of parsed records.
type RecordQueryResponse struct {
	Version string              `json:"version"`
	Records []map[string]any    `json:"records"`
}

// handleRepoRecords godoc
// @Summary      Record query endpoint (Formidable-first)
// @Description  Lists all Formidable records under storage/{template}/ at
// @Description  HEAD. Optional where/sort/limit query params apply the
// @Description  minimal filter DSL from §10.8 — equality/inequality on
// @Description  scalar data.<key> values and numeric range comparisons.
// @Description  Sort accepts a data key, optionally prefixed with "-"
// @Description  for descending. Limit defaults to no limit.
// @Tags         sync
// @Produce      json
// @Param        name      path      string  true   "Repository name"
// @Param        template  path      string  true   "Template directory name under storage/"
// @Param        where     query     string  false  "Filter expression (e.g. city=London, count>5)"
// @Param        sort      query     string  false  "Data key to sort by (prefix with - for descending)"
// @Param        limit     query     int     false  "Maximum number of records"
// @Success      200       {object}  RecordQueryResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      404       {object}  ErrorResponse
// @Failure      405       {object}  ErrorResponse
// @Failure      409       {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /repos/{name}/records/{template} [get]
func (s *Server) handleRepoRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, template, ok := s.resolveRepoRecords(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()

	var where *formidable.Condition
	if expr := strings.TrimSpace(q.Get("where")); expr != "" {
		cond, err := formidable.ParseCondition(expr)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		where = &cond
	}

	limit := 0
	if l := q.Get("limit"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}

	head, err := s.git.Head(name)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	tree, err := s.git.Tree(name, head.Version)
	if err != nil {
		writeSyncError(w, err)
		return
	}

	prefix := "storage/" + template + "/"
	records := make([]formidable.Record, 0)
	for _, entry := range tree.Files {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		if !strings.HasSuffix(entry.Path, ".meta.json") {
			continue
		}
		// Skip anything below the template dir (e.g. images/).
		if strings.Contains(strings.TrimPrefix(entry.Path, prefix), "/") {
			continue
		}
		blob, err := s.git.File(name, head.Version, entry.Path)
		if err != nil {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(blob.ContentB64)
		if err != nil {
			continue
		}
		rec, err := formidable.ParseRecord(raw)
		if err != nil {
			continue
		}
		records = append(records, rec)
	}

	filtered := formidable.FilterRecords(records, where, q.Get("sort"), limit)
	envelopes := make([]map[string]any, 0, len(filtered))
	for _, r := range filtered {
		envelopes = append(envelopes, map[string]any{
			"meta": r.Meta,
			"data": r.Data,
		})
	}

	// Use a json.Encoder with json.Number preservation so numeric
	// scalar fields round-trip as they came in.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(RecordQueryResponse{
		Version: head.Version,
		Records: envelopes,
	})
}

// resolveRepoRecords extracts {name, template} from
// /api/repos/{name}/records/{template}, runs the read-policy +
// existence checks. Template must be a simple directory name (no
// slashes, no dots, non-empty).
func (s *Server) resolveRepoRecords(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	name, rest, found := strings.Cut(trimmed, "/records/")
	if !found || rest == "" {
		writeError(w, http.StatusBadRequest, "template is required")
		return "", "", false
	}
	if strings.Contains(rest, "/") || strings.Contains(rest, "..") {
		writeError(w, http.StatusBadRequest, "template must be a single directory name")
		return "", "", false
	}
	name, ok := s.authorizeRepo(w, r, name, policy.ActionReadRepo)
	if !ok {
		return "", "", false
	}
	return name, rest, true
}
