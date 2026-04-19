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
