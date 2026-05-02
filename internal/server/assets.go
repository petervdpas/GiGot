package server

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Static assets ship as **minified** copies in assets-dist/, generated
// by `go generate ./...` (driven by the directive below). Sources live
// in assets/ — never edit assets-dist/ by hand. Both directories are
// committed so a fresh checkout can `go build` without first running
// generate; CI verifies the dist is current via a re-generate + diff.
//
//go:generate go run ../../cmd/minify-assets

//go:embed assets-dist/*
var assetsFS embed.FS

// assetEntry holds the precomputed bytes for one file: raw + a
// pre-gzipped copy when compression is worthwhile (text/* mostly).
// Computed once at process start so per-request cost is a map lookup
// plus a write — no gzip work in the hot path.
type assetEntry struct {
	contentType string
	raw         []byte
	gz          []byte // nil when gzipping isn't worthwhile (already compressed, or grew)
}

var assetCache = func() map[string]*assetEntry {
	cache := map[string]*assetEntry{}
	sub, err := fs.Sub(assetsFS, "assets-dist")
	if err != nil {
		return cache
	}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(raw)
		}
		cache[p] = &assetEntry{
			contentType: ct,
			raw:         raw,
			gz:          maybeGzip(raw, ct),
		}
		return nil
	})
	return cache
}()

// maybeGzip returns the gzipped form of body when gzip helps, or nil
// when it doesn't (already-compressed image formats, or content that
// gzip happens to grow). Tiny payloads also return nil — the gzip
// header overhead eats any savings under ~150 bytes.
func maybeGzip(body []byte, contentType string) []byte {
	if len(body) < 150 {
		return nil
	}
	if isAlreadyCompressed(contentType) {
		return nil
	}
	var buf bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := gw.Write(body); err != nil {
		return nil
	}
	if err := gw.Close(); err != nil {
		return nil
	}
	if buf.Len() >= len(body) {
		return nil
	}
	return buf.Bytes()
}

func isAlreadyCompressed(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "image/png"),
		strings.HasPrefix(contentType, "image/jpeg"),
		strings.HasPrefix(contentType, "image/webp"),
		strings.HasPrefix(contentType, "image/avif"),
		strings.HasPrefix(contentType, "font/woff"):
		return true
	}
	return false
}

// handleAssets serves /assets/<path> from the embedded minified
// bundle. When the client advertises gzip support we serve a
// precomputed gzipped representation, otherwise the raw bytes.
// Vary: Accept-Encoding is always set so caches don't mix the two.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/assets/")
	if p == "" || strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	entry, ok := assetCache[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Vary", "Accept-Encoding")
	if entry.gz != nil && acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(entry.gz)
		return
	}
	_, _ = w.Write(entry.raw)
}

func acceptsGzip(r *http.Request) bool {
	for enc := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		// Strip optional quality factor — we accept any caller that
		// lists gzip, regardless of priority. Browsers send
		// "gzip, deflate, br" or similar; matching the literal token
		// is enough.
		token := strings.TrimSpace(enc)
		if i := strings.Index(token, ";"); i >= 0 {
			token = token[:i]
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}
