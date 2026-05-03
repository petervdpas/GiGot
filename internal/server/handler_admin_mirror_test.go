package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAdminMirror_Get pins the GET response shape: default cadence
// from config (600s), enabled flag derived from cadence > 0, the
// live enabled-destinations counter, and the heartbeat fields. The
// heartbeat is null/zero on a fresh server (no tick has run yet) —
// nil-LastTickAt is the documented "no ticks yet" signal that the
// admin UI renders as "No poll has run yet."
func TestAdminMirror_Get(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/mirror", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MirrorSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusPollSec != 600 {
		t.Errorf("default StatusPollSec: want 600, got %d", resp.StatusPollSec)
	}
	if !resp.Enabled {
		t.Error("Enabled should be true when StatusPollSec > 0")
	}
	if resp.EnabledDestinations != 0 {
		t.Errorf("idle test server: want 0 destinations, got %d", resp.EnabledDestinations)
	}
	if resp.LastTickAt != nil {
		t.Errorf("fresh server should have no tick yet, got LastTickAt=%v", resp.LastTickAt)
	}
	if resp.LastTickError != "" {
		t.Errorf("fresh server should have empty LastTickError, got %q", resp.LastTickError)
	}
}

// TestAdminMirror_Get_DisabledClearsEnabled — when the cadence is
// 0, Enabled in the response must be false. The toggle in the
// admin UI reads this field directly.
func TestAdminMirror_Get_DisabledClearsEnabled(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.cfg.Mirror.StatusPollSec = 0
	srv.swapStatusPoller(0)

	rec := do(t, srv, http.MethodGet, "/api/admin/mirror", nil, sess)
	var resp MirrorSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Enabled {
		t.Error("Enabled should be false when StatusPollSec=0")
	}
	if resp.StatusPollSec != 0 {
		t.Errorf("StatusPollSec: want 0, got %d", resp.StatusPollSec)
	}
}

// TestAdminMirror_PatchUpdatesPoller pins the operator-tunable path:
// PATCH lands a new poll cadence, the response echoes it, the config
// in memory reflects it, AND the live poller is swapped (we assert
// the goroutine count proxy by checking statusPoller is non-nil
// after a positive value).
func TestAdminMirror_PatchUpdatesPoller(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPatch, "/api/admin/mirror",
		map[string]any{"status_poll_sec": 120}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MirrorSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusPollSec != 120 {
		t.Errorf("echo StatusPollSec: want 120, got %d", resp.StatusPollSec)
	}
	if srv.cfg.Mirror.StatusPollSec != 120 {
		t.Errorf("config.Mirror.StatusPollSec: want 120, got %d", srv.cfg.Mirror.StatusPollSec)
	}
	if srv.statusPoller == nil {
		t.Error("statusPoller should be non-nil after a positive cadence")
	}
	// Tear it down before test exit so the goroutine doesn't leak
	// (test server has no other lifecycle hook).
	srv.swapStatusPoller(0)
}

// TestAdminMirror_PatchZeroDisablesPoller — 0 is the documented
// "disabled" value. The poller goroutine must be torn down and not
// replaced; the manual Refresh button stays as the sole trigger.
func TestAdminMirror_PatchZeroDisablesPoller(t *testing.T) {
	srv, sess := adminTestServer(t)
	// Start with a positive cadence so there's a poller to tear down.
	srv.swapStatusPoller(60)
	if srv.statusPoller == nil {
		t.Fatal("precondition: poller should be running before disable")
	}
	rec := do(t, srv, http.MethodPatch, "/api/admin/mirror",
		map[string]any{"status_poll_sec": 0}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH 0 want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if srv.cfg.Mirror.StatusPollSec != 0 {
		t.Errorf("config.Mirror.StatusPollSec: want 0, got %d", srv.cfg.Mirror.StatusPollSec)
	}
	if srv.statusPoller != nil {
		t.Error("statusPoller should be nil after 0 cadence")
	}
}

// TestAdminMirror_PatchRejectsBadInput — every validation gate.
// After all rejected PATCHes the config sits at the default.
func TestAdminMirror_PatchRejectsBadInput(t *testing.T) {
	srv, sess := adminTestServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty body", map[string]any{}},
		{"negative", map[string]any{"status_poll_sec": -1}},
		{"too high", map[string]any{"status_poll_sec": 86401}},
	}
	for _, c := range cases {
		rec := do(t, srv, http.MethodPatch, "/api/admin/mirror", c.body, sess)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d body=%s", c.name, rec.Code, rec.Body.String())
		}
	}
	if srv.cfg.Mirror.StatusPollSec != 600 {
		t.Errorf("config rolled back: want 600, got %d", srv.cfg.Mirror.StatusPollSec)
	}
}

// TestAdminMirror_RequiresAdminSession — auth fence.
func TestAdminMirror_RequiresAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/mirror", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET without session: want 401, got %d", rec.Code)
	}
}

// TestAdminMirror_MethodNotAllowed — POST/DELETE etc 405 (matches
// limits handler shape).
func TestAdminMirror_MethodNotAllowed(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/admin/mirror", nil, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: want 405, got %d", rec.Code)
	}
}

