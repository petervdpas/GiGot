package server

// Cross-cutting DTOs used by multiple handlers. Per-concern DTOs live in
// their own models_*.go file next to the handler that owns them.

// MessageResponse is a generic plain-language success message.
type MessageResponse struct {
	Message string `json:"message" example:"repository created"`
}

// ErrorResponse is returned on errors.
type ErrorResponse struct {
	Error string `json:"error" example:"repository not found"`
}
