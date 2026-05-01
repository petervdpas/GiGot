package server

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// Help content lives as plain markdown so an editor can drop a new
// topic in without touching templates. Rendered through goldmark on
// first request and cached — embedded files don't change at runtime,
// so the cache is a tiny one-time-render speedup, not a correctness
// concern.

//go:embed help/*.md
var helpFS embed.FS

// helpDoc is the rendered shape passed to the help.html template:
// title pulled from the first H1, body is goldmark output, slug is
// the filename without ".md".
type helpDoc struct {
	PageData
	Slug  string
	Title string
	Body  template.HTML
}

var (
	helpMD = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	helpCacheMu sync.RWMutex
	helpCache   = map[string]*helpDoc{}
)

// renderHelpDoc loads help/<slug>.md, renders it, and caches the
// result. Returns (nil, false) when the slug isn't a known file —
// the handler turns that into a 404. Slugs are validated against
// the embedded directory listing, so a path-traversal attempt
// ("../etc/passwd") simply doesn't match anything.
func renderHelpDoc(slug string) (*helpDoc, bool) {
	helpCacheMu.RLock()
	if d, ok := helpCache[slug]; ok {
		helpCacheMu.RUnlock()
		return d, true
	}
	helpCacheMu.RUnlock()

	raw, err := fs.ReadFile(helpFS, "help/"+slug+".md")
	if err != nil {
		return nil, false
	}

	var buf bytes.Buffer
	if err := helpMD.Convert(raw, &buf); err != nil {
		// A render error on a file we shipped is a programming bug,
		// not user input — surface it as plain-text fallback rather
		// than 500 so the page still loads.
		fmt.Fprintf(&buf, "<pre>%s</pre>", template.HTMLEscapeString(string(raw)))
	}

	doc := &helpDoc{
		Slug:  slug,
		Title: extractTitle(raw, slug),
		Body:  template.HTML(buf.String()),
	}
	helpCacheMu.Lock()
	helpCache[slug] = doc
	helpCacheMu.Unlock()
	return doc, true
}

// extractTitle pulls the first ATX heading from the markdown source
// for the <title> tag. Falls back to the slug if the file has no H1
// — better than a blank tab title for an authoring slip.
func extractTitle(raw []byte, slug string) string {
	for line := range strings.SplitSeq(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(t, "#"))
		}
	}
	return slug
}

// handleHelp serves the help pages. Routes:
//
//	/help           -> index.md
//	/help/          -> index.md
//	/help/<slug>    -> <slug>.md
//
// Anything else (including paths with slashes after /help/) 404s.
// The page is public so an operator can reach it without a session.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	slug := "index"
	if r.URL.Path != "/help" && r.URL.Path != "/help/" {
		rest := strings.TrimPrefix(r.URL.Path, "/help/")
		if rest == "" || strings.ContainsAny(rest, "/\\") {
			http.NotFound(w, r)
			return
		}
		slug = rest
	}

	doc, ok := renderHelpDoc(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	doc.PageData = s.pageData()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := helpTmpl.Execute(w, doc); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}
