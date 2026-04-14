package server

// HealthResponse is returned by the health check endpoint.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}
