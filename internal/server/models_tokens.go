package server

// TokenRequest is the body for issuing a subscription key. Username
// binds the key to an existing Account and accepts two shapes:
//
//   - Scoped   "provider:identifier"   — e.g. "github:petervdpas",
//     "entra:<oid>", "local:alice". Matches the account exactly.
//   - Bare     "identifier"            — shorthand for
//     "local:identifier", kept for back-compat with callers written
//     before the accounts model (integration tests, Postman
//     collection). Any known provider prefix is always interpreted
//     as scoped.
//
// Repo is the single repository this key grants access to —
// subscription keys are one-repo-per-key by design, so a teammate
// who needs access to multiple repos receives one key per repo.
// Abilities is the orthogonal capability list (e.g. "mirror" to
// manage the subscriber-facing destinations API — see
// remote-sync.md §2.6).
type TokenRequest struct {
	Username  string   `json:"username"            example:"github:petervdpas"`
	Repo      string   `json:"repo"                example:"my-templates"`
	Abilities []string `json:"abilities,omitempty" example:"mirror"`
}

// TokenResponse is returned when a token is issued.
type TokenResponse struct {
	Token     string   `json:"token"               example:"a1b2c3d4..."`
	Username  string   `json:"username"            example:"alice"`
	Repo      string   `json:"repo"                example:"my-templates"`
	Abilities []string `json:"abilities,omitempty"`
}

// RevokeTokenRequest is the body for revoking a token.
type RevokeTokenRequest struct {
	Token string `json:"token" example:"a1b2c3d4..."`
}

// UpdateTokenRequest is the body for PATCH /api/admin/tokens. Repo,
// when non-nil and non-empty, rebinds the key to a different repo
// (subject to the "one key per (account, repo)" uniqueness rule).
// Abilities, when non-nil, replaces the list wholesale (pass an
// empty array to clear). Tags, when non-nil, replaces the
// subscription's direct tag set (auto-creating unknown tag names);
// inherited tags from the bound repo or account are not editable
// here — manage those on the parent entity. A nil field is not
// touched.
type UpdateTokenRequest struct {
	Token     string    `json:"token"`
	Repo      *string   `json:"repo,omitempty"`
	Abilities *[]string `json:"abilities,omitempty"`
	Tags      *[]string `json:"tags,omitempty"`
}
