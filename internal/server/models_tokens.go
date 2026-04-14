package server

// TokenRequest is the body for issuing a token.
type TokenRequest struct {
	Username string `json:"username" example:"alice"`
}

// TokenResponse is returned when a token is issued.
type TokenResponse struct {
	Token    string `json:"token" example:"a1b2c3d4..."`
	Username string `json:"username" example:"alice"`
}

// RevokeTokenRequest is the body for revoking a token.
type RevokeTokenRequest struct {
	Token string `json:"token" example:"a1b2c3d4..."`
}
