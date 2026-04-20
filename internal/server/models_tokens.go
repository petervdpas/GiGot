package server

// TokenRequest is the body for issuing a token. Username binds the
// key to an existing Account and accepts two shapes:
//
//   - Scoped   "provider:identifier"   — e.g. "github:petervdpas",
//     "entra:<oid>", "local:alice". Matches the account exactly.
//   - Bare     "identifier"            — shorthand for
//     "local:identifier", kept for back-compat with callers written
//     before the accounts model (integration tests, Postman
//     collection). Any known provider prefix is always interpreted
//     as scoped.
//
// Repos is the allowlist of repository names the subscription key
// may access; Abilities is the orthogonal capability list (e.g.
// "mirror" to manage the subscriber-facing destinations API — see
// remote-sync.md §2.6). Omit or pass an empty array for either to
// issue a key without that scope.
type TokenRequest struct {
	// Username is "provider:identifier" (preferred) or "identifier"
	// (shorthand for local:identifier). See type docs.
	Username  string   `json:"username"            example:"github:petervdpas"`
	Repos     []string `json:"repos,omitempty"     example:"my-templates,shared-context"`
	Abilities []string `json:"abilities,omitempty" example:"mirror"`
}

// TokenResponse is returned when a token is issued.
type TokenResponse struct {
	Token     string   `json:"token"               example:"a1b2c3d4..."`
	Username  string   `json:"username"            example:"alice"`
	Repos     []string `json:"repos,omitempty"`
	Abilities []string `json:"abilities,omitempty"`
}

// RevokeTokenRequest is the body for revoking a token.
type RevokeTokenRequest struct {
	Token string `json:"token" example:"a1b2c3d4..."`
}

// UpdateTokenRequest is the body for PATCH /api/admin/tokens. Any field
// left nil (i.e. absent from the JSON) is not touched; a non-nil field
// replaces the corresponding allowlist wholesale (pass an empty array to
// clear it). Pointer-to-slice is deliberate — a plain slice can't
// distinguish "omitted" from "set to empty", which would make partial
// updates ambiguous.
type UpdateTokenRequest struct {
	Token     string    `json:"token"`
	Repos     *[]string `json:"repos,omitempty"`
	Abilities *[]string `json:"abilities,omitempty"`
}
