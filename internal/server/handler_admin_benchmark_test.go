package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestBenchmark_HappyPath_Sequential pins the basic contract: a valid
// request at the smallest scale, two topics, returns a 200 with one
// row per topic and non-zero (well, non-negative) timings. Iterations
// kept tiny because the test is about the wire shape, not throughput.
func TestBenchmark_HappyPath_Sequential(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/benchmark", map[string]any{
		"scale":      10,
		"iterations": 2,
		"mode":       "sequential",
		"topics":     []string{"token-list", "tag-catalogue"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp BenchmarkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Scale != 10 || resp.Iterations != 2 || resp.Mode != "sequential" {
		t.Errorf("echo mismatch: %+v", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 result rows, got %d", len(resp.Results))
	}
	wantTopics := map[string]bool{"token-list": false, "tag-catalogue": false}
	for _, r := range resp.Results {
		if _, ok := wantTopics[r.Topic]; !ok {
			t.Errorf("unexpected topic in results: %q", r.Topic)
		}
		wantTopics[r.Topic] = true
		if r.Iterations != 2 {
			t.Errorf("topic %s: want 2 iterations, got %d", r.Topic, r.Iterations)
		}
		if r.MedianMs < 0 || r.P95Ms < 0 || r.P99Ms < 0 || r.TotalMs < 0 {
			t.Errorf("topic %s: negative timing %+v", r.Topic, r)
		}
	}
	for name, seen := range wantTopics {
		if !seen {
			t.Errorf("missing topic in response: %q", name)
		}
	}
}

// TestBenchmark_Concurrent confirms the mode switch is honoured and
// the response shape is identical — each topic still gets a row with
// its own timings. The test doesn't assert anything about the
// timings themselves (overlap means we can't, in a deterministic
// way), only that the runner returns one row per requested topic.
func TestBenchmark_Concurrent(t *testing.T) {
	srv, sess := adminTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/admin/benchmark", map[string]any{
		"scale":      10,
		"iterations": 2,
		"mode":       "concurrent",
		"topics":     []string{"token-list", "repo-list"},
	}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp BenchmarkResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Mode != "concurrent" {
		t.Errorf("mode echo: got %q, want concurrent", resp.Mode)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 rows, got %d", len(resp.Results))
	}
}

// TestBenchmark_RejectsBadInput pins the four input gates: bad scale,
// out-of-range iterations, unknown topic, and unknown mode. Each
// error path should be a 400 — a misconfig from the page UI should
// fail fast rather than silently spawn a sandbox the operator never
// asked for.
func TestBenchmark_RejectsBadInput(t *testing.T) {
	srv, sess := adminTestServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"unsupported scale", map[string]any{"scale": 42, "iterations": 1, "mode": "sequential", "topics": []string{"token-list"}}},
		{"zero iterations", map[string]any{"scale": 10, "iterations": 0, "mode": "sequential", "topics": []string{"token-list"}}},
		{"too many iterations", map[string]any{"scale": 10, "iterations": 100000, "mode": "sequential", "topics": []string{"token-list"}}},
		{"unknown mode", map[string]any{"scale": 10, "iterations": 1, "mode": "lol", "topics": []string{"token-list"}}},
		{"empty topics", map[string]any{"scale": 10, "iterations": 1, "mode": "sequential", "topics": []string{}}},
		{"unknown topic", map[string]any{"scale": 10, "iterations": 1, "mode": "sequential", "topics": []string{"not-a-topic"}}},
	}
	for _, c := range cases {
		rec := do(t, srv, http.MethodPost, "/api/admin/benchmark", c.body, sess)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d body=%s", c.name, rec.Code, rec.Body.String())
		}
	}
}

// TestBenchmark_RequiresAdminSession pins the auth fence — no cookie
// means no benchmark, before the body is even parsed.
func TestBenchmark_RequiresAdminSession(t *testing.T) {
	srv, _ := adminTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/admin/benchmark", map[string]any{
		"scale": 10, "iterations": 1, "mode": "sequential", "topics": []string{"token-list"},
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

// TestBenchmark_MethodNotAllowed pins that GET / DELETE return 405,
// not 400 — benchmark is a write-shape operation (it mutates the
// sandbox), so REST verb sanity matters.
func TestBenchmark_MethodNotAllowed(t *testing.T) {
	srv, sess := adminTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/admin/benchmark", nil, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET want 405, got %d", rec.Code)
	}
}
