package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// formidableFS embeds the Formidable-context starter files. The `all:` prefix
// ensures dotfiles (like storage/.gitkeep) are included; the default go:embed
// behaviour strips files starting with '.' or '_'.
//
//go:embed all:scaffold/formidable
var formidableFS embed.FS

// formidableScaffoldRoot is the embed root path — the prefix we strip when
// mapping an embedded file to its location in the target repo.
const formidableScaffoldRoot = "scaffold/formidable"

// formidableMarkerPath is the Phase 0 marker a formidable_first server looks
// for to decide whether to apply schema-aware behaviour (see
// docs/design/structured-sync-api.md §2.5).
const formidableMarkerPath = ".formidable/context.json"

// formidableMarkerVersion is the current schema version of the marker file.
// Bump if the shape changes incompatibly.
const formidableMarkerVersion = 1

// formidableScaffoldFiles walks the embedded Formidable scaffold and returns
// the file set the scaffolder should commit into a fresh repo. The marker
// file .formidable/context.json is generated on the fly with scaffoldedAt so
// the commit carries the actual scaffold time. Paths are rooted at the repo,
// not at the embed tree (i.e. "templates/basic.yaml", not
// "scaffold/formidable/templates/basic.yaml").
func formidableScaffoldFiles(scaffoldedAt time.Time) ([]gitmanager.ScaffoldFile, error) {
	var out []gitmanager.ScaffoldFile
	err := fs.WalkDir(formidableFS, formidableScaffoldRoot, func(path string, d fs.DirEntry, err error) error {
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
		rel := strings.TrimPrefix(path, formidableScaffoldRoot+"/")
		out = append(out, gitmanager.ScaffoldFile{
			Path:    rel,
			Content: data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("formidable scaffold is empty (embed broken?)")
	}

	marker, err := buildFormidableMarker(scaffoldedAt)
	if err != nil {
		return nil, err
	}
	out = append(out, gitmanager.ScaffoldFile{
		Path:    formidableMarkerPath,
		Content: marker,
	})
	return out, nil
}

func buildFormidableMarker(scaffoldedAt time.Time) ([]byte, error) {
	payload := map[string]any{
		"version":       formidableMarkerVersion,
		"scaffolded_by": "gigot",
		"scaffolded_at": scaffoldedAt.UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal formidable marker: %w", err)
	}
	return append(data, '\n'), nil
}

// Scaffold committer identity. Hardcoded on purpose — if it ever needs to be
// configurable, move it to config.CryptoConfig or a dedicated ScaffoldConfig.
const (
	scaffoldCommitterName  = "GiGot Scaffolder"
	scaffoldCommitterEmail = "scaffold@gigot.local"
	scaffoldCommitMessage  = "Initialize Formidable context"
)
