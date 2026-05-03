package server

import "net/http"

// handleHealthLoad godoc
// @Summary      Load gauge — current GiGot load classification
// @Description  Returns a coarse load classification (`low` / `medium`
// @Description  / `high`) plus the raw signals that drove it: number
// @Description  of in-flight git operations, p95 / p99 of completed
// @Description  durations across the last 60 seconds, and the count
// @Description  of samples that were in the rolling window when the
// @Description  snapshot was taken.
// @Description
// @Description  Same Level value is reflected on every response via
// @Description  the `X-GiGot-Load` header — clients that want the
// @Description  gauge as a side-effect of normal traffic should read
// @Description  the header rather than poll this endpoint. Polling
// @Description  is for explicit health probes and dashboards.
// @Description
// @Description  Public (no auth) so an external monitor (Azure
// @Description  Monitor, a Formidable instance reading without a
// @Description  session) can scrape it. Reveals operational state
// @Description  but no user-or-content data — the same posture as
// @Description  GET /api/health.
// @Tags        system
// @Produce      json
// @Success      200  {object}  LoadSnapshot
// @Router       /health/load [get]
func (s *Server) handleHealthLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snap := LoadSnapshot{}
	if s.load != nil {
		snap = s.load.Snapshot()
	}
	if s.pushSlots != nil {
		inUse, cap := s.pushSlots.Snapshot()
		snap.PushSlotInUse = inUse
		snap.PushSlotCapacity = cap
	}
	writeJSON(w, http.StatusOK, snap)
}
