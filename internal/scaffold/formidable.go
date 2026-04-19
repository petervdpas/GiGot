// Package scaffold provides the Formidable-context starter files committed
// into a freshly-created repo when a caller opts into
// scaffold_formidable. Extracted from internal/server so the CLI can seed
// a scaffolded repo without depending on the HTTP handler graph.
package scaffold

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// formidableFS embeds the Formidable-context starter files. The `all:`
// prefix ensures dotfiles (like storage/.gitkeep) are included; the
// default go:embed behaviour strips files starting with '.' or '_'.
//
//go:embed all:formidable
var formidableFS embed.FS

const formidableRoot = "formidable"

// Formidable marker paths + constants. Single source of truth for the
// server handlers, the CLI demo flow, and anything else that needs to
// read or write the context marker.
const (
	// MarkerPath is where a valid Formidable context marker lives.
	MarkerPath = ".formidable/context.json"
	// MarkerVersion is the current schema version of the marker file.
	// Bump if the shape changes incompatibly.
	MarkerVersion = 1

	CommitterName  = "GiGot Scaffolder"
	CommitterEmail = "scaffold@gigot.local"
	CommitMessage  = "Initialize Formidable context"
	MarkerMessage  = "Add Formidable context marker"
)

// FormidableFiles walks the embedded Formidable scaffold and returns the
// file set to commit into a fresh repo. The marker file is generated on
// the fly with scaffoldedAt so the commit carries the real scaffold
// time. Paths are rooted at the repo, not at the embed tree.
func FormidableFiles(scaffoldedAt time.Time) ([]gitmanager.ScaffoldFile, error) {
	var out []gitmanager.ScaffoldFile
	err := fs.WalkDir(formidableFS, formidableRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := formidableFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		rel := strings.TrimPrefix(path, formidableRoot+"/")
		out = append(out, gitmanager.ScaffoldFile{Path: rel, Content: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("formidable scaffold is empty (embed broken?)")
	}
	marker, err := BuildMarker(scaffoldedAt)
	if err != nil {
		return nil, err
	}
	out = append(out, gitmanager.ScaffoldFile{Path: MarkerPath, Content: marker})
	return out, nil
}

// BuildMarker returns the canonical .formidable/context.json payload.
func BuildMarker(scaffoldedAt time.Time) ([]byte, error) {
	payload := map[string]any{
		"version":       MarkerVersion,
		"scaffolded_by": "gigot",
		"scaffolded_at": scaffoldedAt.UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal formidable marker: %w", err)
	}
	return append(data, '\n'), nil
}

// IsValidMarker decides whether a blob already at MarkerPath should be
// treated as "marker already present". The rule is deliberately narrow
// (parses as JSON + non-zero version field) so a corrupt marker gets
// replaced rather than preserved — see docs/design/structured-sync-api.md §2.7.
func IsValidMarker(data []byte) bool {
	var m struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return m.Version >= 1
}
