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

	RemoteStatus     string                  `json:"remote_status,omitempty"`
	RemoteCheckedAt  *time.Time              `json:"remote_checked_at,omitempty"`
	RemoteCheckError string                  `json:"remote_check_error,omitempty"`
	RemoteRefs       []RemoteRefStatusView   `json:"remote_refs,omitempty"`
}

// RemoteRefStatusView is the wire shape of one per-ref entry in the
// remote-status breakdown. Mirrors destinations.RemoteRefStatus 1:1 but
// keeps a distinct type at the wire so the storage type can evolve
// without leaking into swagger.
type RemoteRefStatusView struct {
	Ref    string `json:"ref"`
	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	State  string `json:"state"`
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
