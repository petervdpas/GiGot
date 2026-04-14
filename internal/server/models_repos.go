package server

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
