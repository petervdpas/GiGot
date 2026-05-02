package server

import "time"

// CreateTagRequest is the body of POST /api/admin/tags.
type CreateTagRequest struct {
	Name string `json:"name"`
}

// RenameTagRequest is the body of PATCH /api/admin/tags/{name}. Only
// the new name is mutable today; future fields can extend this struct
// without breaking existing clients.
type RenameTagRequest struct {
	Name string `json:"name"`
}

// TagView is the wire-format tag returned on list / get / create /
// rename responses. ID is the stable handle every assignment row
// references; Name is the display string an admin reads.
type TagView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	Usage     TagUsage  `json:"usage"`
}

// TagUsage reports the direct (non-inherited) reference count for a
// tag, split by entity. Inherited references are not counted here —
// they belong to the entity that carries the explicit assignment.
type TagUsage struct {
	Repos         int `json:"repos"`
	Subscriptions int `json:"subscriptions"`
	Accounts      int `json:"accounts"`
}

// TagListResponse is the body of GET /api/admin/tags.
type TagListResponse struct {
	Tags  []TagView `json:"tags"`
	Count int       `json:"count"`
}

// TagDeleteResponse echoes the cascade sweep counts so the audit log
// and UI confirmation both see exactly what was removed.
type TagDeleteResponse struct {
	Deleted TagView  `json:"deleted"`
	Swept   TagUsage `json:"swept"`
}

// TagSweepUnusedResponse reports the names removed by the
// "remove unused" admin action. Empty list + zero count is the
// nothing-to-do happy path; the UI shows a muted "no unused tags"
// hint instead of a confirmation toast.
type TagSweepUnusedResponse struct {
	Removed []string `json:"removed"`
	Count   int      `json:"count"`
}
