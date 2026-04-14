package server

// HealthResponse is returned by the health check endpoint.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// RepoInfo describes a repository.
type RepoInfo struct {
	Name string `json:"name" example:"my-templates"`
	Path string `json:"path" example:"repos/my-templates.git"`
}

// RepoListResponse is returned when listing repositories.
type RepoListResponse struct {
	Repos []RepoInfo `json:"repos"`
	Count int        `json:"count" example:"2"`
}

// CreateRepoRequest is the body for creating a repository.
type CreateRepoRequest struct {
	Name string `json:"name" example:"my-templates"`
}

// MessageResponse is a generic message response.
type MessageResponse struct {
	Message string `json:"message" example:"repository created"`
}

// ErrorResponse is returned on errors.
type ErrorResponse struct {
	Error string `json:"error" example:"repository not found"`
}

// TokenRequest is the body for issuing a token.
type TokenRequest struct {
	Username string   `json:"username" example:"alice"`
	Roles    []string `json:"roles" example:"admin,reader"`
}

// TokenResponse is returned when a token is issued.
type TokenResponse struct {
	Token    string   `json:"token" example:"a1b2c3d4..."`
	Username string   `json:"username" example:"alice"`
	Roles    []string `json:"roles" example:"admin,reader"`
}

// RevokeTokenRequest is the body for revoking a token.
type RevokeTokenRequest struct {
	Token string `json:"token" example:"a1b2c3d4..."`
}

// ServerPubKeyResponse exposes the server's NaCl public key. Clients use this
// to seal request bodies before sending them to the server.
type ServerPubKeyResponse struct {
	PublicKey string `json:"public_key" example:"base64-encoded-32-byte-key"`
}

// EnrollRequest registers a client's NaCl public key with the server.
type EnrollRequest struct {
	ClientID  string `json:"client_id"  example:"laptop-01"`
	PublicKey string `json:"public_key" example:"base64-encoded-32-byte-key"`
}

// EnrollResponse confirms enrollment and hands back the server's public key
// so the client can seal subsequent requests without a second round trip.
type EnrollResponse struct {
	ClientID        string `json:"client_id"`
	ServerPublicKey string `json:"server_public_key"`
}

// AdminLoginRequest is the body for /admin/login.
type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AdminLoginResponse confirms a successful admin login.
type AdminLoginResponse struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// TokenListItem describes one issued token in an admin listing.
type TokenListItem struct {
	Token    string   `json:"token"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// TokenListResponse is the body of GET /api/admin/tokens.
type TokenListResponse struct {
	Tokens []TokenListItem `json:"tokens"`
	Count  int             `json:"count"`
}
