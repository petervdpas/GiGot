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
//
// When ScaffoldFormidable is true, the new repo is seeded with an initial
// commit that lays out a Formidable context (templates/ with a starter
// basic.yaml, an empty storage/, and a README). Otherwise the repo is left
// empty — a raw bare git repository with no commits.
type CreateRepoRequest struct {
	Name               string `json:"name"                example:"my-templates"`
	ScaffoldFormidable bool   `json:"scaffold_formidable" example:"false"`
}
