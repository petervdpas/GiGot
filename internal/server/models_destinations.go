package server

import "time"

// CreateDestinationRequest is the body of
// POST /api/admin/repos/{name}/destinations.
// URL and CredentialName are required; Enabled defaults to true when
// omitted so that a freshly-created destination is live without a
// second call.
type CreateDestinationRequest struct {
	URL            string `json:"url"`
	CredentialName string `json:"credential_name"`
	Enabled        *bool  `json:"enabled,omitempty"`
}

// UpdateDestinationRequest is the body of
// PATCH /api/admin/repos/{name}/destinations/{id}.
// All fields are pointer-optional: a nil pointer means "do not change
// this field." A non-nil empty URL or CredentialName is rejected (use
// DELETE to remove a destination).
type UpdateDestinationRequest struct {
	URL            *string `json:"url,omitempty"`
	CredentialName *string `json:"credential_name,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

// DestinationView is the wire-format destination returned on list /
// get / create / update responses.
type DestinationView struct {
	ID             string     `json:"id"`
	URL            string     `json:"url"`
	CredentialName string     `json:"credential_name"`
	Enabled        bool       `json:"enabled"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty"`
	LastSyncStatus string     `json:"last_sync_status,omitempty"`
	LastSyncError  string     `json:"last_sync_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// DestinationListResponse is the body of
// GET /api/admin/repos/{name}/destinations.
type DestinationListResponse struct {
	Destinations []DestinationView `json:"destinations"`
	Count        int               `json:"count"`
}

// CredentialDeleteConflictResponse is the body of
// DELETE /api/admin/credentials/{name} when the credential is still
// referenced by one or more repo destinations. RefRepos is the list of
// repo names the admin needs to retarget or clear before the
// credential can be removed.
type CredentialDeleteConflictResponse struct {
	Error    string   `json:"error"`
	RefRepos []string `json:"ref_repos"`
}
