package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// TestAdminLimits_Get pins the GET response shape: defaults from
// config (10 / 5) plus a live in-use counter.
func TestAdminLimits_Get(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/limits", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp LimitsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PushSlots != 10 {
		t.Errorf("default PushSlots: want 10, got %d", resp.PushSlots)
	}
	if resp.PushRetryAfterSec != 5 {
		t.Errorf("default PushRetryAfterSec: want 5, got %d", resp.PushRetryAfterSec)
	}
	if resp.PushSlotInUse != 0 {
		t.Errorf("idle: want 0 in_use, got %d", resp.PushSlotInUse)
	}
}

// TestAdminLimits_PatchUpdatesSlotPool pins the operator-tunable
// path end-to-end: PATCH lands a new push_slots value, the response
// echoes it, and the live slot pool is resized so future acquires
// honor the new cap.
func TestAdminLimits_PatchUpdatesSlotPool(t *testing.T) {
	srv, sess := adminTestServer(t)
	newSlots := 3
	rec := do(t, srv, http.MethodPatch, "/api/admin/limits",
		map[string]any{"push_slots": newSlots}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp LimitsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PushSlots != newSlots {
		t.Errorf("echo PushSlots: want %d, got %d", newSlots, resp.PushSlots)
	}
	if _, c := srv.pushSlots.Snapshot(); c != newSlots {
		t.Errorf("live slot pool capacity: want %d, got %d", newSlots, c)
	}
	// Retry-after wasn't in the body — should be unchanged.
	if resp.PushRetryAfterSec != 5 {
		t.Errorf("PushRetryAfterSec untouched: want 5, got %d", resp.PushRetryAfterSec)
	}
}

// TestAdminLimits_PatchUpdatesRetryAfter pins the second knob: the
// retry-after value lands in cfg.Limits and shows up on the next
// 429 response from the slot gate.
func TestAdminLimits_PatchUpdatesRetryAfter(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodPatch, "/api/admin/limits",
		map[string]any{"push_retry_after_sec": 30}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp LimitsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PushRetryAfterSec != 30 {
		t.Errorf("echo PushRetryAfterSec: want 30, got %d", resp.PushRetryAfterSec)
	}
	if srv.cfg.Limits.PushRetryAfterSec != 30 {
		t.Errorf("config.Limits.PushRetryAfterSec: want 30, got %d", srv.cfg.Limits.PushRetryAfterSec)
	}
}

// TestAdminLimits_PatchRejectsBadInput pins the validation gates.
// Each error path leaves the running config unchanged.
func TestAdminLimits_PatchRejectsBadInput(t *testing.T) {
	srv, sess := adminTestServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty body", map[string]any{}},
		{"slots too low", map[string]any{"push_slots": 0}},
		{"slots too high", map[string]any{"push_slots": 1001}},
		{"retry too low", map[string]any{"push_retry_after_sec": 0}},
		{"retry too high", map[string]any{"push_retry_after_sec": 3601}},
	}
	for _, c := range cases {
		rec := do(t, srv, http.MethodPatch, "/api/admin/limits", c.body, sess)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d body=%s", c.name, rec.Code, rec.Body.String())
		}
	}
	// After all the rejected PATCHes the config should still be at defaults.
	if srv.cfg.Limits.PushSlots != 10 {
		t.Errorf("config rolled back: PushSlots want 10, got %d", srv.cfg.Limits.PushSlots)
	}
	if srv.cfg.Limits.PushRetryAfterSec != 5 {
		t.Errorf("config rolled back: PushRetryAfterSec want 5, got %d", srv.cfg.Limits.PushRetryAfterSec)
	}
}

// TestAdminLimits_RequiresAdminSession pins the auth fence.
func TestAdminLimits_RequiresAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/limits", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET without session: want 401, got %d", rec.Code)
	}
}

// TestSlotGate_Returns429WhenFull pins the end-to-end admission
// gate behaviour: when all slots are taken, a new git-receive-pack
// request comes back with 429 and a Retry-After header carrying the
// configured value. We don't actually push real git data — we just
// hit the receive-pack URL on a server whose pool is already
// saturated by an out-of-band TryAcquire, so the router decides
// rejection before reaching the handler.
func TestSlotGate_Returns429WhenFull(t *testing.T) {
	srv, _ := adminTestServer(t)
	srv.git.InitBare("addresses")

	// Saturate the pool: with default cap=10, take all 10 slots.
	for i := 0; i < 10; i++ {
		if !srv.pushSlots.TryAcquire() {
			t.Fatalf("setup: failed to take slot %d", i)
		}
	}

	rec := do(t, srv, http.MethodPost, "/git/addresses/git-receive-pack", nil, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := rec.Header().Get("Retry-After")
	if got == "" {
		t.Fatal("Retry-After header missing")
	}
	if n, err := strconv.Atoi(got); err != nil || n != 5 {
		t.Errorf("Retry-After: want 5 (default), got %q", got)
	}
}

// TestSlotGate_RejectionUsesConfiguredRetryAfter pins that the
// Retry-After header value follows cfg.Limits.PushRetryAfterSec —
// admin tunable, not hardcoded.
func TestSlotGate_RejectionUsesConfiguredRetryAfter(t *testing.T) {
	srv, sess := adminTestServer(t)
	srv.git.InitBare("addresses")

	// Set retry-after to a non-default value via the API, exercising
	// the patch path, not just direct config mutation.
	if rec := do(t, srv, http.MethodPatch, "/api/admin/limits",
		map[string]any{"push_retry_after_sec": 17}, sess); rec.Code != http.StatusOK {
		t.Fatalf("PATCH retry-after: %d", rec.Code)
	}

	// Saturate.
	for i := 0; i < 10; i++ {
		srv.pushSlots.TryAcquire()
	}

	rec := do(t, srv, http.MethodPost, "/git/addresses/git-receive-pack", nil, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "17" {
		t.Errorf("Retry-After: want 17, got %q", got)
	}
}
