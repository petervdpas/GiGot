package server

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

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

// formidableScaffoldFiles walks the embedded Formidable scaffold and returns
// the file set the scaffolder should commit into a fresh repo. Paths are
// rooted at the repo, not at the embed tree (i.e. "templates/basic.yaml",
// not "scaffold/formidable/templates/basic.yaml").
func formidableScaffoldFiles() ([]gitmanager.ScaffoldFile, error) {
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
	return out, nil
}

// Scaffold committer identity. Hardcoded on purpose — if it ever needs to be
// configurable, move it to config.CryptoConfig or a dedicated ScaffoldConfig.
const (
	scaffoldCommitterName  = "GiGot Scaffolder"
	scaffoldCommitterEmail = "scaffold@gigot.local"
	scaffoldCommitMessage  = "Initialize Formidable context"
)
