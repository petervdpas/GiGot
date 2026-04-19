package server

import (
	"encoding/base64"
	"errors"
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
