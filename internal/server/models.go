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
