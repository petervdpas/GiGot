package server

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/scaffold"
)

// Re-exports and thin wrappers around internal/scaffold so existing
// server-package code keeps its current identifiers while the embed +
// payload logic lives in one leaf package reachable from both server
// and cli. Delete these aliases if server-package callers are updated
// to reference scaffold.* directly.

const (
	formidableMarkerPath   = scaffold.MarkerPath
	scaffoldCommitterName  = scaffold.CommitterName
	scaffoldCommitterEmail = scaffold.CommitterEmail
	scaffoldCommitMessage  = scaffold.CommitMessage
	markerStampMessage     = scaffold.MarkerMessage
)

func formidableScaffoldFiles(scaffoldedAt time.Time) ([]gitmanager.ScaffoldFile, error) {
	return scaffold.FormidableFiles(scaffoldedAt)
}

func buildFormidableMarker(scaffoldedAt time.Time) ([]byte, error) {
	return scaffold.BuildMarker(scaffoldedAt)
}

func isValidFormidableMarker(data []byte) bool {
	return scaffold.IsValidMarker(data)
}

// stampFormidableMarker idempotently ensures the target repo carries a
// valid .formidable/context.json on HEAD. Returns (true, nil) when a
// stamp commit was written, (false, nil) when a valid marker was already
// present. Caller (handleCreateRepo) guarantees the repo exists and is
// non-empty — empty-repo callers should use the scaffold path instead.
//
// Composition only: reuses Manager.File for the absence check and
// Manager.WriteFile for the write, so ref-update semantics (CAS, author
// identity, message trailer handling) stay in one place per §2.7.1.
func stampFormidableMarker(git *gitmanager.Manager, name string, scaffoldedAt time.Time) (bool, error) {
	existing, err := git.File(name, "", formidableMarkerPath)
	if err == nil {
		raw, decodeErr := base64.StdEncoding.DecodeString(existing.ContentB64)
		if decodeErr == nil && isValidFormidableMarker(raw) {
			return false, nil
		}
		// Broken or unparseable marker — fall through and overwrite.
	} else if !errors.Is(err, gitmanager.ErrPathNotFound) {
		return false, err
	}

	marker, err := buildFormidableMarker(scaffoldedAt)
	if err != nil {
		return false, err
	}

	_, err = git.WriteFile(name, gitmanager.WriteOptions{
		ParentVersion:  "HEAD",
		Path:           formidableMarkerPath,
		Content:        marker,
		AuthorName:     scaffoldCommitterName,
		AuthorEmail:    scaffoldCommitterEmail,
		CommitterName:  scaffoldCommitterName,
		CommitterEmail: scaffoldCommitterEmail,
		Message:        markerStampMessage,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// ensureFormidableShape idempotently brings a repo to full Formidable
// shape: valid context marker + at least one file under templates/ +
// at least one file under storage/. Each missing piece is filled with
// the embedded scaffold starter (`templates/basic.yaml`,
// `storage/.gitkeep`); existing files are never touched. README.md is
// deliberately never added on convert — a repo being converted already
// owns whatever README it has.
//
// Returns the list of paths actually written (nil when the repo was
// already shaped), so the caller can decide whether to emit an audit
// entry and what to tell the admin. All missing pieces land in one
// atomic commit so the tree doesn't briefly exist in a half-scaffolded
// state.
func ensureFormidableShape(git *gitmanager.Manager, name string, scaffoldedAt time.Time) ([]string, error) {
	head, err := git.Head(name)
	if err != nil {
		return nil, err
	}
	tree, err := git.Tree(name, head.Version)
	if err != nil {
		return nil, err
	}

	hasMarker := false
	hasTemplates := false
	hasStorage := false
	for _, e := range tree.Files {
		switch {
		case e.Path == formidableMarkerPath:
			// Only count the marker if it actually parses — a broken
			// marker gets replaced the same way stampFormidableMarker
			// treats it.
			if f, ferr := git.File(name, "", formidableMarkerPath); ferr == nil {
				if raw, derr := base64.StdEncoding.DecodeString(f.ContentB64); derr == nil && isValidFormidableMarker(raw) {
					hasMarker = true
				}
			}
		case strings.HasPrefix(e.Path, "templates/"):
			hasTemplates = true
		case strings.HasPrefix(e.Path, "storage/"):
			hasStorage = true
		}
	}

	scaffoldFiles, err := formidableScaffoldFiles(scaffoldedAt)
	if err != nil {
		return nil, err
	}
	// Index the embedded scaffold by path so we can pull exactly the
	// starter pieces we need without duplicating content here.
	scaffoldByPath := make(map[string][]byte, len(scaffoldFiles))
	for _, f := range scaffoldFiles {
		scaffoldByPath[f.Path] = f.Content
	}

	var changes []gitmanager.Change
	var added []string

	addFromScaffold := func(path string) error {
		content, ok := scaffoldByPath[path]
		if !ok {
			return errors.New("scaffold is missing " + path)
		}
		changes = append(changes, gitmanager.Change{
			Op: gitmanager.OpPut, Path: path, Content: content,
		})
		added = append(added, path)
		return nil
	}

	if !hasMarker {
		marker, mErr := buildFormidableMarker(scaffoldedAt)
		if mErr != nil {
			return nil, mErr
		}
		changes = append(changes, gitmanager.Change{
			Op: gitmanager.OpPut, Path: formidableMarkerPath, Content: marker,
		})
		added = append(added, formidableMarkerPath)
	}
	if !hasTemplates {
		if aerr := addFromScaffold("templates/basic.yaml"); aerr != nil {
			return nil, aerr
		}
	}
	if !hasStorage {
		if aerr := addFromScaffold("storage/.gitkeep"); aerr != nil {
			return nil, aerr
		}
	}

	if len(changes) == 0 {
		return nil, nil
	}

	_, err = git.Commit(name, gitmanager.CommitOptions{
		ParentVersion:  "HEAD",
		Changes:        changes,
		AuthorName:     scaffoldCommitterName,
		AuthorEmail:    scaffoldCommitterEmail,
		CommitterName:  scaffoldCommitterName,
		CommitterEmail: scaffoldCommitterEmail,
		Message:        scaffoldCommitMessage,
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}
