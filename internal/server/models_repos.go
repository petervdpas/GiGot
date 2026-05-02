package server

// RepoInfo describes a repository. Enrichment fields (Head,
// DefaultBranch, HasFormidable, DestinationCount) are populated by
// the list endpoint so the admin UI can render rich cards in a
// single round-trip — a repo that has never been written to (bare,
// no commits) reports Empty=true and leaves the head fields zero.
type RepoInfo struct {
	Name             string   `json:"name" example:"my-templates"`
	Path             string   `json:"path" example:"repos/my-templates.git"`
	Empty            bool     `json:"empty" example:"false"`
	Commits          int      `json:"commits" example:"42"`
	Head             string   `json:"head,omitempty" example:"a1b2c3d4e5f6"`
	DefaultBranch    string   `json:"default_branch,omitempty" example:"main"`
	HasFormidable    bool     `json:"has_formidable" example:"false"`
	DestinationCount int      `json:"destination_count" example:"0"`
	Tags             []string `json:"tags,omitempty"`
}

// RepoListResponse is returned when listing repositories.
type RepoListResponse struct {
	Repos []RepoInfo `json:"repos"`
	Count int        `json:"count" example:"2"`
}

// CreateRepoRequest is the body for creating a repository.
//
// ScaffoldFormidable is tri-state (see docs/design/structured-sync-api.md
// §2.7). Omitted (nil) ⇒ use the server-level default from
// server.formidable_first. true ⇒ always stamp .formidable/context.json
// (on init as the initial scaffold commit, on clone as a single commit on
// top, idempotent if the cloned tree already carries a valid marker).
// false ⇒ never stamp, regardless of server mode — the escape hatch for
// hosting a plain repo on a Formidable-first server or mirroring a plain
// upstream.
//
// When SourceURL is set, the repo is created by cloning the given git URL
// instead of initialising an empty bare repo. Combining SourceURL with
// ScaffoldFormidable=true is explicitly supported — clone then stamp.
type CreateRepoRequest struct {
	Name               string `json:"name"                example:"my-templates"`
	ScaffoldFormidable *bool  `json:"scaffold_formidable,omitempty" example:"false"`
	SourceURL          string `json:"source_url"          example:"https://github.com/owner/repo.git"`
}
