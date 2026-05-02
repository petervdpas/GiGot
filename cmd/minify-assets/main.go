// Command minify-assets walks internal/server/assets/, minifies every
// text-based file (JS, CSS, HTML, SVG), copies binary assets through
// untouched (PNG, ico, fonts), and writes the result to a sibling
// internal/server/assets-dist/ directory.
//
// Run via `go generate ./...` (the //go:generate directive in
// internal/server/assets.go drives it) or directly: `go run
// ./cmd/minify-assets`. The output directory is committed to git so
// fresh checkouts can `go build` without first having to install
// generate-time dependencies.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/svg"
)

const (
	mode    = 0o644
	dirMode = 0o755
)

func main() {
	// Defaults match the //go:generate context (cwd is the directory
	// containing the directive, so plain "assets" / "assets-dist"
	// resolve correctly). Flags let `go run ./cmd/minify-assets` from
	// any cwd work too — pass -src/-dst with whatever paths fit.
	var (
		srcDir = flag.String("src", "assets", "source directory (relative to cwd)")
		dstDir = flag.String("dst", "assets-dist", "destination directory (relative to cwd)")
	)
	flag.Parse()

	src := *srcDir
	dst := *dstDir

	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("text/html", html.Minify)
	m.AddFunc("application/javascript", js.Minify)
	m.AddFunc("image/svg+xml", svg.Minify)

	// Wipe and recreate dist so deleted source files don't leave
	// stale copies behind. The directory is generated output — its
	// only contents are whatever this command writes.
	if err := os.RemoveAll(dst); err != nil {
		fatalf("clean dst: %v", err)
	}
	if err := os.MkdirAll(dst, dirMode); err != nil {
		fatalf("create dst: %v", err)
	}

	var savings int64
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), dirMode); err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mt := mediaTypeFor(path)
		var dst []byte
		if mt != "" {
			b, err := m.Bytes(mt, raw)
			if err != nil {
				return fmt.Errorf("minify %s: %w", rel, err)
			}
			dst = b
		} else {
			// Binary or unknown: copy through unchanged.
			dst = raw
		}
		if err := os.WriteFile(out, dst, mode); err != nil {
			return err
		}
		saved := int64(len(raw)) - int64(len(dst))
		savings += saved
		if mt != "" {
			fmt.Printf("  %-32s %6d -> %6d  (-%d)\n", rel, len(raw), len(dst), saved)
		} else {
			fmt.Printf("  %-32s %6d  (copied)\n", rel, len(raw))
		}
		return nil
	})
	if err != nil {
		fatalf("walk: %v", err)
	}
	fmt.Printf("\ntotal savings: %d bytes\n", savings)
}

// mediaTypeFor returns the minifier media-type for a file, or "" when
// the file should be copied through unchanged.
func mediaTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	case ".html", ".htm":
		return "text/html"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "minify-assets: "+format+"\n", args...)
	io.WriteString(os.Stderr, "")
	os.Exit(1)
}
