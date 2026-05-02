package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// LimitsResponse is the body returned by GET /api/admin/limits and
// echoed by PATCH on success. Combines the persisted config (the
// two operator knobs) with the live in-flight count so the admin
// UI can show "currently 3 / 10 slots in use" without a second
// round-trip to /api/health/load.
type LimitsResponse struct {
	PushSlots         int `json:"push_slots" example:"10"`
	PushRetryAfterSec int `json:"push_retry_after_sec" example:"5"`
	PushSlotInUse     int `json:"push_slot_in_use" example:"3"`
}

// LimitsRequest is the body of PATCH /api/admin/limits. Both fields
// are pointer-optional so a partial update — change push_slots but
// leave push_retry_after_sec alone — sends only the field being
// changed. nil pointer means "do not change this field."
type LimitsRequest struct {
	PushSlots         *int `json:"push_slots,omitempty"`
	PushRetryAfterSec *int `json:"push_retry_after_sec,omitempty"`
}

// handleAdminLimits godoc
// @Summary      Read or update server-side admission limits (admin only)
// @Description  GET returns the current LimitsConfig plus live
// @Description  in-flight slot count. PATCH updates push_slots
// @Description  and/or push_retry_after_sec; both fields are
// @Description  optional, omitted fields are left unchanged.
// @Description  Successful PATCH persists the new values to the
// @Description  config file (cfg.Save) so changes survive restart,
// @Description  and resizes the live slot pool — in-flight pushes
// @Description  finish on the old capacity, new pushes are gated
// @Description  by the new one. Bounds: push_slots 1-1000,
// @Description  push_retry_after_sec 1-3600.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      LimitsRequest   false  "Patch body (PATCH only)"
// @Success      200   {object}  LimitsResponse  "GET / PATCH response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/limits [get]
// @Router       /admin/limits [patch]
func (s *Server) handleAdminLimits(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminGetLimits(w, r)
	case http.MethodPatch:
		s.adminPatchLimits(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminGetLimits(w http.ResponseWriter, _ *http.Request) {
	inUse := 0
	if s.pushSlots != nil {
		inUse, _ = s.pushSlots.Snapshot()
	}
	writeJSON(w, http.StatusOK, LimitsResponse{
		PushSlots:         s.cfg.Limits.PushSlots,
		PushRetryAfterSec: s.cfg.Limits.PushRetryAfterSec,
		PushSlotInUse:     inUse,
	})
}

// adminPatchLimits applies a partial update. Both fields validated
// against the documented bounds before any persistence happens, so
// a 400 leaves the running config unchanged. On success the slot
// pool is resized AND the file is rewritten; if the file write
// fails we log it but still serve the (now-applied) new value —
// matches the /admin/auth ReloadAuth pattern (apply first, persist
// best-effort) so a transient disk problem doesn't roll back the
// admin's just-applied tunable.
func (s *Server) adminPatchLimits(w http.ResponseWriter, r *http.Request) {
	var req LimitsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PushSlots == nil && req.PushRetryAfterSec == nil {
		writeError(w, http.StatusBadRequest, "at least one field must be provided")
		return
	}
	if req.PushSlots != nil {
		if *req.PushSlots < 1 || *req.PushSlots > 1000 {
			writeError(w, http.StatusBadRequest, "push_slots must be between 1 and 1000")
			return
		}
	}
	if req.PushRetryAfterSec != nil {
		if *req.PushRetryAfterSec < 1 || *req.PushRetryAfterSec > 3600 {
			writeError(w, http.StatusBadRequest, "push_retry_after_sec must be between 1 and 3600")
			return
		}
	}

	if req.PushSlots != nil {
		s.cfg.Limits.PushSlots = *req.PushSlots
		if s.pushSlots != nil {
			s.pushSlots.Resize(*req.PushSlots)
		}
	}
	if req.PushRetryAfterSec != nil {
		s.cfg.Limits.PushRetryAfterSec = *req.PushRetryAfterSec
	}

	if s.cfg.Path != "" {
		if err := s.cfg.Save(s.cfg.Path); err != nil {
			log.Printf("server: PATCH /api/admin/limits: persist to %s: %v", s.cfg.Path, err)
		}
	}

	s.adminGetLimits(w, r)
}
