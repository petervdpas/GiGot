package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthLoad_HappyPath pins the wire shape of GET /api/health/load
// on an idle server: status 200, JSON body with the LoadSnapshot
// fields populated, level=low. This is the contract Formidable
// reads to decide whether to back off.
func TestHealthLoad_HappyPath(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health/load", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var snap LoadSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if snap.Level != "low" {
		t.Errorf("idle server: want level=low, got %q", snap.Level)
	}
	if snap.InFlight != 0 {
		t.Errorf("idle server: want in_flight=0, got %d", snap.InFlight)
	}
}

// TestHealthLoad_RejectsNonGet pins method-not-allowed on POST/etc —
// the endpoint is a read-only gauge. Unlike most endpoints we
// don't list method routing in a multi-verb table, so this fence
// is defensive against future "add PATCH to update thresholds"
// drift that should be a separate endpoint.
func TestHealthLoad_RejectsNonGet(t *testing.T) {
	srv := testServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/health/load", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: want 405, got %d", method, rec.Code)
		}
	}
}

// TestHealthLoad_HeaderOnEveryResponse pins that every response (not
// just the load endpoint itself) carries `X-GiGot-Load` — including
// the existing /api/health, the index page, and 404s. Formidable
// reads this header off any sync to gauge load without having to
// poll the dedicated endpoint.
func TestHealthLoad_HeaderOnEveryResponse(t *testing.T) {
	srv := testServer(t)
	cases := []struct {
		path string
		want int
	}{
		{"/api/health", http.StatusOK},
		{"/", http.StatusOK},
		{"/this-path-doesnt-exist", http.StatusNotFound},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: want %d, got %d", c.path, c.want, rec.Code)
		}
		got := rec.Header().Get("X-GiGot-Load")
		if got == "" {
			t.Errorf("%s: missing X-GiGot-Load header", c.path)
			continue
		}
		switch got {
		case "low", "medium", "high":
			// ok
		default:
			t.Errorf("%s: X-GiGot-Load value %q outside {low,medium,high}", c.path, got)
		}
	}
}

// TestHealthLoad_ReflectsTrackerState confirms the snapshot tracks
// reality: pump the in-flight counter directly via the tracker and
// the snapshot endpoint reports the new state. This catches a
// future refactor that accidentally creates a second tracker
// instance and reads from the wrong one.
func TestHealthLoad_ReflectsTrackerState(t *testing.T) {
	srv := testServer(t)

	// Bump the tracker out of band to simulate an active operation.
	start := srv.load.Begin()
	defer srv.load.End(start)

	req := httptest.NewRequest(http.MethodGet, "/api/health/load", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var snap LoadSnapshot
	json.Unmarshal(rec.Body.Bytes(), &snap)
	if snap.InFlight != 1 {
		t.Errorf("want in_flight=1 (tracker was bumped), got %d", snap.InFlight)
	}
}
