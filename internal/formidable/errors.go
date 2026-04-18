// Package formidable implements structured merge for Formidable record
// files (storage/<template>/*.meta.json). It is a leaf package: it does
// not import anything else in the project and has no side effects.
//
// The merge rule is uniform across all field types — every data field
// is treated as an atomic value and last-writer-wins (by meta.updated)
// resolves any both-sides-changed disagreement. See
// docs/design/structured-sync-api.md §10.2–§10.3.
package formidable

import "errors"

var ErrMalformedRecord = errors.New("formidable: malformed record")

// FieldConflict is one entry in a RecordConflict. In F1 Scope is always
// "meta" — data fields never produce conflicts under the uniform rule.
// The field is kept for forward-compat with future strictness modes.
type FieldConflict struct {
	Scope  string `json:"scope"`
	Key    string `json:"key"`
	Reason string `json:"reason,omitempty"`
}

// RecordConflict is the 409 body shape from §10.6. In F1 it only
// appears for immutable-meta violations on created / id / template.
type RecordConflict struct {
	Path           string          `json:"path"`
	CurrentVersion string          `json:"current_version,omitempty"`
	FieldConflicts []FieldConflict `json:"field_conflicts"`
}
