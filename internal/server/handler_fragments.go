package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Fragments are client-rendered HTML partials served raw to GG.lazy
// (see docs/design/lazy.md). They live alongside the existing
// server-rendered full-page templates but never go through the Go
// `html/template` parser — placeholder substitution happens
// client-side, against API response data.
//
// Two clean conventions live in templates/ now:
//
//   - templates/*.html              — full pages, server-rendered
//   - templates/fragments/*.html    — partials, client-rendered
//
// Each file is processed by exactly one engine; both use double
// braces but they don't overlap because no fragment is ever fetched
// through the page-template path and no page is ever fetched
// through /fragments/.
//
//go:embed templates/fragments/*.html
var fragmentsFS embed.FS

// fragmentEntry holds one fragment's bytes plus the strong ETag
// derived from its content, AND a precomputed gzipped copy when
// compression earns its keep. Computed once at startup so the
// per-request cost is a map lookup + a write — no gzip work in the
// hot path. Same pattern assets.go uses for the static bundle.
type fragmentEntry struct {
	body []byte
	gz   []byte // nil when gzip didn't shrink the body, e.g. tiny fragments
	etag string
}

// fragmentCache is name → entry. Names are the file's basename
// without the .html suffix (so abilities.html is served at
// /fragments/abilities). Built once at startup; the map is
// read-only after that, no lock needed.
var fragmentCache = func() map[string]*fragmentEntry {
	cache := map[string]*fragmentEntry{}
	sub, err := fs.Sub(fragmentsFS, "templates/fragments")
	if err != nil {
		return cache
	}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".html") {
			return nil
		}
		body, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		name := strings.TrimSuffix(path.Base(p), ".html")
		// ETag is `"<sha256-prefix>"` per RFC 7232 §2.3 — quoted,
		// strong validator. Browsers send this back on
		// `If-None-Match` and we 304 when it matches.
		sum := sha256.Sum256(body)
		cache[name] = &fragmentEntry{
			body: body,
			gz:   maybeGzip(body, "text/html"),
			etag: `"` + hex.EncodeToString(sum[:16]) + `"`,
		}
		return nil
	})
	return cache
}()

// handleFragments godoc
// @Summary      Serve a UI fragment template (admin only)
// @Description  Returns the raw HTML of the named fragment from
// @Description  internal/server/templates/fragments/. Used by the
// @Description  GG.lazy client helper to render detail panes on
// @Description  demand. Admin-session gated — fragments don't carry
// @Description  user data but they encode admin-UI shape (which
// @Description  inputs exist, what gets PATCHed where), and a
// @Description  leak gives an attacker recon for free.
// @Description
// @Description  Cache: strong ETag derived from the fragment body's
// @Description  SHA-256. Browsers send `If-None-Match` on every
// @Description  load and get a 304 after the first fetch — net cost
// @Description  per fragment per release is one tiny round trip.
// @Tags         admin
// @Produce      html
// @Param        name  path      string  true  "Fragment name (no path, no extension)"
// @Success      200   {string}  string  "Fragment HTML body"
// @Success      304   {string}  string  "Not Modified — ETag matched"
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse  "Unknown fragment name"
// @Failure      405   {object}  ErrorResponse
// @Router       /fragments/{name} [get]
func (s *Server) handleFragments(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/fragments/")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid fragment name")
		return
	}
	entry, ok := fragmentCache[name]
	if !ok {
		writeError(w, http.StatusNotFound, "fragment not found")
		return
	}
	// `Cache-Control: no-cache` makes the browser revalidate every
	// load (sending If-None-Match) but lets it serve from disk cache
	// after the 304. Net effect: one round trip per fragment per
	// release, no full body re-download.
	w.Header().Set("ETag", entry.etag)
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Vary: Accept-Encoding so caches don't mix gzipped + raw bodies
	// for the same URL — same posture as handleAssets.
	w.Header().Set("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match == entry.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if entry.gz != nil && acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(entry.gz)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.body)
}
