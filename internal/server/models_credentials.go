package server

import "time"

// CreateCredentialRequest is the body of POST /api/admin/credentials.
// Name, Kind, and Secret are required; Expires and Notes are optional.
type CreateCredentialRequest struct {
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`
	Secret  string     `json:"secret"`
	Expires *time.Time `json:"expires,omitempty"`
	Notes   string     `json:"notes,omitempty"`
}

// UpdateCredentialRequest is the body of PATCH /api/admin/credentials/{name}.
// All fields are pointer-optional: a nil pointer means "do not change this
// field." A non-nil empty Secret or Kind is rejected (use DELETE to remove
// a credential). Notes can be set to an empty string to clear it.
type UpdateCredentialRequest struct {
	Kind    *string    `json:"kind,omitempty"`
	Secret  *string    `json:"secret,omitempty"`
	Expires *time.Time `json:"expires,omitempty"`
	Notes   *string    `json:"notes,omitempty"`
}

// CredentialView is the wire-format credential returned on list/get/create/
// update responses. Deliberately does not include Secret — that value never
// leaves the server after it's been stored.
type CredentialView struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Expires   *time.Time `json:"expires,omitempty"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Notes     string     `json:"notes,omitempty"`
}

// CredentialListResponse is the body of GET /api/admin/credentials.
type CredentialListResponse struct {
	Credentials []CredentialView `json:"credentials"`
	Count       int              `json:"count"`
}

// CredentialNameRef is a non-sensitive view exposing only the fields a
// maintainer needs to reference a vault entry when wiring a mirror —
// the human name and the kind. Returned by GET /api/credentials/names
// so subscriber-side UIs (Formidable's mirror form) can autocomplete
// without ever seeing secrets, expiry, or last-used metadata. See
// accounts.md §1 (maintainer role) and remote-sync.md §2.6.
type CredentialNameRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// CredentialNameListResponse is the body of GET /api/credentials/names.
type CredentialNameListResponse struct {
	Credentials []CredentialNameRef `json:"credentials"`
	Count       int                 `json:"count"`
}
