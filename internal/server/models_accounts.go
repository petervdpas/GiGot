package server

import "time"

// AccountView is the wire-format account returned on list / get /
// create / update responses. PasswordHash is intentionally omitted —
// it never leaves the server. SubscriptionCount lets the accounts
// console show, per row, how many active subscription keys point at
// this account (see docs/design/accounts.md §6); 0 renders as a
// muted dash in the UI.
type AccountView struct {
	Provider          string    `json:"provider"`
	Identifier        string    `json:"identifier"`
	Role              string    `json:"role"`
	DisplayName       string    `json:"display_name,omitempty"`
	Email             string    `json:"email,omitempty"`
	HasPassword       bool      `json:"has_password"`
	SubscriptionCount int       `json:"subscription_count"`
	Tags              []string  `json:"tags,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// AccountListResponse is the body of GET /api/admin/accounts.
type AccountListResponse struct {
	Accounts []AccountView `json:"accounts"`
	Count    int           `json:"count"`
}

// CreateAccountRequest is the body of POST /api/admin/accounts. Admin
// creates an account in any provider. For local accounts, Password is
// optional — an admin may provision an account without a password and
// the holder sets one later via /register (same username) or a future
// password-reset flow. For non-local providers, Password is ignored.
type CreateAccountRequest struct {
	Provider    string `json:"provider"`
	Identifier  string `json:"identifier"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Password    string `json:"password,omitempty"`
}

// UpdateAccountRequest is the body of
// PATCH /api/admin/accounts/{provider}/{identifier}. Nil fields are
// left unchanged. Role may only be set to admin or regular (closed
// set). Password, when non-nil and non-empty, resets the local
// account's bcrypt hash; ignored on non-local accounts.
type UpdateAccountRequest struct {
	Role        *string `json:"role,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Password    *string `json:"password,omitempty"`
}

// RegisterRequest is the body of POST /api/register. Self-service
// registration for local accounts only; always produces role=regular.
type RegisterRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

// BindTokenRequest is the body of POST /api/admin/tokens/bind — the
// "bind legacy token to account" action on the subscriptions page.
// Creates a role=regular local account for the token's stored username
// if one doesn't already exist. Idempotent.
type BindTokenRequest struct {
	Token string `json:"token"`
}
