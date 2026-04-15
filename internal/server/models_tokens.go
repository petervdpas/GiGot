package server

// TokenRequest is the body for issuing a token. Repos is the allowlist of
// repository names the subscription key may access; omit or pass an empty
// array to issue a key with no repo access (admin can attach repos later).
type TokenRequest struct {
	Username string   `json:"username"       example:"alice"`
	Repos    []string `json:"repos,omitempty" example:"my-templates,shared-context"`
}

// TokenResponse is returned when a token is issued.
type TokenResponse struct {
	Token    string   `json:"token"          example:"a1b2c3d4..."`
	Username string   `json:"username"       example:"alice"`
	Repos    []string `json:"repos,omitempty"`
}

// RevokeTokenRequest is the body for revoking a token.
type RevokeTokenRequest struct {
	Token string `json:"token" example:"a1b2c3d4..."`
}

// UpdateTokenReposRequest changes the repo allowlist on an existing token.
type UpdateTokenReposRequest struct {
	Token string   `json:"token"`
	Repos []string `json:"repos"`
}
