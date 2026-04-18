package server

import (
	"encoding/base64"
	"path"
	"strings"

	"github.com/petervdpas/GiGot/internal/formidable"
)

// isFormidableRecordPath returns true when p matches
// storage/<template>/<name>.meta.json at exactly two directory levels.
// Matches the §10.1 path contract and rejects attempts to sneak
// .meta.json files into other locations.
func isFormidableRecordPath(p string) bool {
	if strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return false
	}
	if !strings.HasSuffix(p, ".meta.json") {
		return false
	}
	if !strings.HasPrefix(p, "storage/") {
		return false
	}
	// Expect exactly: storage / <template-dir> / <file>.meta.json
	parts := strings.Split(p, "/")
	if len(parts) != 3 {
		return false
	}
	if parts[1] == "" || parts[2] == "" || parts[1] == "images" {
		return false
	}
	return path.Base(p) == parts[2]
}

// isFormidableRepoAtHEAD returns true when the repo currently carries a
// valid .formidable/context.json marker. Thin wrapper around the
// existing isValidFormidableMarker so the gate moves with the marker
// contract in §2.5.
func (s *Server) isFormidableRepoAtHEAD(name string) bool {
	info, err := s.git.File(name, "", formidableMarkerPath)
	if err != nil {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(info.ContentB64)
	if err != nil {
		return false
	}
	return isValidFormidableMarker(raw)
}

// maybeFormidableMerge attempts a structured record merge before the
// generic write path runs. Returns:
//
//   - (merged, nil, headVersion, true, nil) on a successful per-field
//     merge. Caller should rewrite opts.Content = merged and
//     opts.ParentVersion = headVersion so WriteFile fast-forwards, then
//     surface MergedFrom/MergedWith in the response using the values
//     the caller already has (client's original parent + headVersion).
//   - (nil, conflict, "", true, nil) when an immutable meta key was
//     violated. Caller emits 409 with the conflict body.
//   - (nil, nil, "", false, nil) when this write is not a Formidable
//     record candidate (no marker, non-record path, fast-forward
//     already, malformed existing blobs). Caller continues on the
//     generic path.
//
// Error return is reserved for transport-level problems: a malformed
// incoming record surfaces as applicable=false so the generic path can
// produce the same client error it always did.
func (s *Server) maybeFormidableMerge(
	repo, filePath string,
	parentVersion string,
	incoming []byte,
) (merged []byte, conflict *formidable.RecordConflict, headVersion string, applicable bool, err error) {
	if !isFormidableRecordPath(filePath) {
		return nil, nil, "", false, nil
	}
	if !s.isFormidableRepoAtHEAD(repo) {
		return nil, nil, "", false, nil
	}

	head, err := s.git.Head(repo)
	if err != nil {
		// Empty repo or similar — let the generic path surface the error.
		return nil, nil, "", false, nil
	}
	if parentVersion == head.Version {
		// Fast-forward case — no merge needed. Generic path handles it.
		return nil, nil, "", false, nil
	}

	baseBlob, baseErr := s.git.File(repo, parentVersion, filePath)
	theirsBlob, theirsErr := s.git.File(repo, head.Version, filePath)

	// If either side can't be read (e.g. the record was just added and
	// doesn't exist in one commit), defer to the generic path which
	// handles add/add and delete/modify uniformly.
	if baseErr != nil || theirsErr != nil {
		return nil, nil, "", false, nil
	}

	baseBytes, err := base64.StdEncoding.DecodeString(baseBlob.ContentB64)
	if err != nil {
		return nil, nil, "", false, nil
	}
	theirsBytes, err := base64.StdEncoding.DecodeString(theirsBlob.ContentB64)
	if err != nil {
		return nil, nil, "", false, nil
	}

	base, err := formidable.ParseRecord(baseBytes)
	if err != nil {
		return nil, nil, "", false, nil
	}
	theirs, err := formidable.ParseRecord(theirsBytes)
	if err != nil {
		return nil, nil, "", false, nil
	}
	yours, err := formidable.ParseRecord(incoming)
	if err != nil {
		// Incoming is malformed — let the generic path handle it.
		return nil, nil, "", false, nil
	}

	result, err := formidable.Merge(filePath, base, theirs, yours)
	if err != nil {
		return nil, nil, "", false, err
	}
	if result.Conflict != nil {
		result.Conflict.CurrentVersion = head.Version
		return nil, result.Conflict, head.Version, true, nil
	}
	return result.Merged, nil, head.Version, true, nil
}

