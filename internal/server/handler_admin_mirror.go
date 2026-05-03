package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// MirrorSettingsResponse is the body returned by GET /api/admin/mirror
// and echoed by PATCH on success. status_poll_sec is the persisted
// cadence; enabled_destinations is the live count of destinations
// the poller would currently visit on a tick (across every repo,
// enabled flag honoured) so the admin UI can render a "currently
// checking N destinations" line without a second round-trip.
type MirrorSettingsResponse struct {
	StatusPollSec        int `json:"status_poll_sec" example:"600"`
	EnabledDestinations  int `json:"enabled_destinations" example:"3"`
}

// MirrorSettingsRequest is the body of PATCH /api/admin/mirror.
// status_poll_sec is pointer-optional so a caller that only wants to
// update other future fields doesn't have to echo this one. 0 is a
// valid value (disables polling); negatives reject.
type MirrorSettingsRequest struct {
	StatusPollSec *int `json:"status_poll_sec,omitempty"`
}

// mirrorPollMu guards the swap of s.statusPoller. Only PATCH
// /api/admin/mirror writes here; the poller goroutine itself never
// touches the field after newMirrorStatusPoller returns. Lazy-init
// via sync.Once would also work, but a plain Mutex on the Server is
// simpler and the contention is zero in practice (admin clicks Save).
var mirrorPollMu sync.Mutex

// handleAdminMirror godoc
// @Summary      Read or update mirror-related operator settings
// @Description  GET returns the persisted cadence + live count of
// @Description  enabled destinations. PATCH validates and applies
// @Description  status_poll_sec, hot-swaps the background poller
// @Description  (stops the old one, starts a new one with the
// @Description  configured cadence; 0 disables and tears the poller
// @Description  down without replacement), and persists to gigot.json.
// @Description  Bound: 0 ≤ status_poll_sec ≤ 86400 (one day cap;
// @Description  longer cadences should disable the poller and rely
// @Description  on the manual Refresh button instead).
// @Tags        system
// @Accept       json
// @Produce      json
// @Param        body  body      MirrorSettingsRequest   false  "Patch body (PATCH only)"
// @Success      200   {object}  MirrorSettingsResponse  "GET / PATCH response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security    SessionAuth
// @Router       /admin/mirror [get]
// @Router       /admin/mirror [patch]
func (s *Server) handleAdminMirror(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminGetMirror(w, r)
	case http.MethodPatch:
		s.adminPatchMirror(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminGetMirror(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, MirrorSettingsResponse{
		StatusPollSec:       s.cfg.Mirror.StatusPollSec,
		EnabledDestinations: len(s.enabledDestinations()),
	})
}

// adminPatchMirror applies the new cadence: stops the existing
// poller (if any) and starts a fresh one with the new value.
// Persistence follows the /admin/limits + /admin/auth precedent —
// apply first, then best-effort cfg.Save so a transient disk problem
// doesn't roll back the operator's just-applied setting.
func (s *Server) adminPatchMirror(w http.ResponseWriter, r *http.Request) {
	var req MirrorSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.StatusPollSec == nil {
		writeError(w, http.StatusBadRequest, "at least one field must be provided")
		return
	}
	v := *req.StatusPollSec
	if v < 0 || v > 86400 {
		writeError(w, http.StatusBadRequest, "status_poll_sec must be between 0 and 86400 (0 disables)")
		return
	}

	s.cfg.Mirror.StatusPollSec = v
	s.swapStatusPoller(v)

	if s.cfg.Path != "" {
		if err := s.cfg.Save(s.cfg.Path); err != nil {
			log.Printf("server: PATCH /api/admin/mirror: persist to %s: %v", s.cfg.Path, err)
		}
	}

	s.adminGetMirror(w, r)
}

// swapStatusPoller stops the running poller (if any) and starts a
// fresh one if the new cadence is positive. Idempotent and safe to
// call from any goroutine. A zero cadence tears the poller down
// without replacement so the manual Refresh button is the only
// remaining trigger.
func (s *Server) swapStatusPoller(seconds int) {
	mirrorPollMu.Lock()
	defer mirrorPollMu.Unlock()
	if s.statusPoller != nil {
		s.statusPoller.Stop()
		s.statusPoller = nil
	}
	if seconds <= 0 {
		return
	}
	s.statusPoller = newMirrorStatusPoller(
		time.Duration(seconds)*time.Second,
		func() []repoDestination { return s.enabledDestinations() },
		func(ctx context.Context, repo, id string) {
			if err := s.refreshRemoteStatus(ctx, repo, id); err != nil {
				log.Printf("mirror-status: poll repo=%q dest=%q: %v", repo, id, err)
			}
		},
	)
}
