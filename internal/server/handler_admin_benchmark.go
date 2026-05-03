package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/config"
)

// Benchmark sub-system — admin-triggered server-side micro-bench.
//
// Always runs against a fresh sandbox, never against live data: the
// point is to characterise the machine the server is running on
// (latency, contention, GC, sealed-store crypto cost on this CPU),
// independent of whatever data happens to be on the production
// instance today. A run typically tells the operator one of two
// things: "this hardware is fine for the next year of growth" or
// "we should plan an upgrade before crossing N subs."
//
// Each request:
//   1. mkdtemp → fresh data directory
//   2. server.New(cfg) against the temp dir
//   3. seed N synthetic accounts + N subs + 1 repo + 5 tags with a
//      rough distribution
//   4. for each selected topic, run `iterations` calls (sequential
//      OR concurrent — see Mode), measure with time.Since
//   5. per-topic stats: median / p95 / p99 / total
//   6. rmrf the temp dir, return results
//
// Concurrency mode runs all selected topics in parallel goroutines —
// each topic still records its own per-iteration timings, but the
// timings include cross-topic contention (locks on the token store,
// GC pressure, etc.) so an operator can see whether the hardware
// holds up under simultaneous load.

// BenchmarkRequest is the body of POST /api/admin/benchmark.
type BenchmarkRequest struct {
	Scale      int      `json:"scale"`      // 10 | 100 | 500 | 1000
	Iterations int      `json:"iterations"` // per-topic call count
	Mode       string   `json:"mode"`       // "sequential" | "concurrent"
	Topics     []string `json:"topics"`     // names from benchmarkTopics
}

// BenchmarkTopicResult is one row in the response — per-topic timing
// summary across `Iterations` runs of that topic.
type BenchmarkTopicResult struct {
	Topic      string  `json:"topic"`
	Iterations int     `json:"iterations"`
	MedianMs   float64 `json:"median_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	TotalMs    float64 `json:"total_ms"`
}

// BenchmarkResponse is the body returned by POST /api/admin/benchmark.
type BenchmarkResponse struct {
	Scale      int                    `json:"scale"`
	Mode       string                 `json:"mode"`
	Iterations int                    `json:"iterations"`
	SetupMs    float64                `json:"setup_ms"`
	Results    []BenchmarkTopicResult `json:"results"`
}

// benchmarkScaleAllowed pins the four supported synthetic-data sizes.
// Larger sizes are rejected so a typo can't accidentally seed a
// million subs (which on an underpowered VM could OOM the server).
// Add a new bucket here if a future tier wants 5000+.
var benchmarkScaleAllowed = map[int]struct{}{
	10:   {},
	100:  {},
	500:  {},
	1000: {},
}

// benchmarkTopicFn runs ONE iteration of a topic against the
// sandboxed server. The runner wraps it with timing + iteration loop.
type benchmarkTopicFn func(s *Server)

// benchmarkTopics is the registry of measurable operations. Names
// must match the strings the client toggles send. Each function
// closes over the sandboxed `*Server` (passed in) so all reads land
// against the synthetic seed without leaking caches across topics.
var benchmarkTopics = map[string]benchmarkTopicFn{
	"token-list": func(s *Server) {
		// Mirrors GET /api/admin/tokens (no filter): pull every
		// entry, build the wire-shape items.
		entries := s.tokenStrategy.List()
		items := make([]TokenListItem, 0, len(entries))
		for _, e := range entries {
			provider, identifier, perr := parseTokenUsername(e.Username)
			has := perr == nil && s.accounts.Has(provider, identifier)
			items = append(items, TokenListItem{
				Token:      e.Token,
				Username:   e.Username,
				Repo:       e.Repo,
				Abilities:  e.Abilities,
				HasAccount: has,
			})
		}
		_ = items
	},
	"token-list-filtered": func(s *Server) {
		// Mirrors GET /api/admin/tokens?tag=bench-tag-a — the chip
		// filter hot path. Computes effective tags per sub and
		// applies the OR-union match (effectiveCoversAny). One
		// synthetic chip is chosen so the filter actually narrows.
		want := []string{"bench-tag-a"}
		entries := s.tokenStrategy.List()
		items := make([]TokenListItem, 0, len(entries))
		for _, e := range entries {
			provider, identifier, _ := parseTokenUsername(e.Username)
			accountKey := ""
			if s.accounts.Has(provider, identifier) {
				accountKey = provider + ":" + identifier
			}
			effective := s.tags.EffectiveSubscriptionTags(e.Token, e.Repo, accountKey)
			if !effectiveCoversAny(effective, want) {
				continue
			}
			items = append(items, TokenListItem{Token: e.Token})
		}
		_ = items
	},
	"repo-list": func(s *Server) {
		_, _ = s.git.List()
	},
	"account-list": func(s *Server) {
		_ = s.accounts.List()
	},
	"tag-catalogue": func(s *Server) {
		_ = s.tags.All()
	},
	"effective-tags-per-sub": func(s *Server) {
		// Inner of the chip-filter loop, isolated so an operator
		// can see how much of the filtered listing's cost is the
		// per-sub tag union vs. everything else.
		entries := s.tokenStrategy.List()
		for _, e := range entries {
			provider, identifier, _ := parseTokenUsername(e.Username)
			accountKey := ""
			if s.accounts.Has(provider, identifier) {
				accountKey = provider + ":" + identifier
			}
			_ = s.tags.EffectiveSubscriptionTags(e.Token, e.Repo, accountKey)
		}
	},
}

// handleAdminBenchmark godoc
// @Summary      Run a server-side micro-benchmark suite (admin only)
// @Description  Spins up a fresh sandbox `*Server` against a temp
// @Description  directory, seeds it with N synthetic accounts + subs +
// @Description  tags, runs the requested topics for the requested
// @Description  iteration count, tears down the sandbox, and returns
// @Description  per-topic timing summaries (median / p95 / p99 / total).
// @Description
// @Description  Always runs against synthetic data — the point is to
// @Description  characterise the host hardware, not the live dataset.
// @Description  Mode "sequential" runs each topic in turn; "concurrent"
// @Description  runs all selected topics in parallel goroutines so the
// @Description  timings include cross-topic contention.
// @Tags        system
// @Accept       json
// @Produce      json
// @Param        body  body      BenchmarkRequest   true  "Benchmark request"
// @Success      200   {object}  BenchmarkResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Security    SessionAuth
// @Router       /admin/benchmark [post]
func (s *Server) handleAdminBenchmark(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req BenchmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := benchmarkScaleAllowed[req.Scale]; !ok {
		writeError(w, http.StatusBadRequest, "scale must be 10, 100, 500, or 1000")
		return
	}
	if req.Iterations < 1 || req.Iterations > 10_000 {
		writeError(w, http.StatusBadRequest, "iterations must be between 1 and 10000")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "sequential"
	}
	if mode != "sequential" && mode != "concurrent" {
		writeError(w, http.StatusBadRequest, `mode must be "sequential" or "concurrent"`)
		return
	}
	if len(req.Topics) == 0 {
		writeError(w, http.StatusBadRequest, "topics list cannot be empty")
		return
	}
	for _, t := range req.Topics {
		if _, ok := benchmarkTopics[t]; !ok {
			writeError(w, http.StatusBadRequest, "unknown topic: "+t)
			return
		}
	}

	setupStart := time.Now()
	sandbox, teardown, err := buildBenchmarkSandbox(req.Scale)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sandbox setup failed: "+err.Error())
		return
	}
	defer teardown()
	setupMs := float64(time.Since(setupStart).Microseconds()) / 1000.0

	results := runBenchmark(r.Context(), sandbox, req.Topics, req.Iterations, mode)

	// Client may have aborted mid-run (Cancel button → fetch's
	// AbortController → connection close → r.Context cancelled). The
	// runners exit early in that case, so writing a body would race
	// with the closed socket — and the client isn't listening anyway.
	// Skip the response; the browser already saw the abort.
	if err := r.Context().Err(); err != nil {
		return
	}

	writeJSON(w, http.StatusOK, BenchmarkResponse{
		Scale:      req.Scale,
		Mode:       mode,
		Iterations: req.Iterations,
		SetupMs:    setupMs,
		Results:    results,
	})
}

// buildBenchmarkSandbox creates a temp data directory, a fresh
// `*Server` against it, seeds the synthetic dataset, and returns the
// server plus a teardown closure that the handler defers. Errors
// from teardown are logged via the closure's host process; the
// handler doesn't surface them since the request itself succeeded.
func buildBenchmarkSandbox(scale int) (*Server, func(), error) {
	dir, err := os.MkdirTemp("", "gigot-bench-*")
	if err != nil {
		return nil, nil, fmt.Errorf("mkdtemp: %w", err)
	}
	teardown := func() { _ = os.RemoveAll(dir) }

	cfg := config.Defaults()
	cfg.Storage.RepoRoot = dir
	cfg.Crypto.PrivateKeyPath = filepath.Join(dir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(dir, "server.pub")
	cfg.Crypto.DataDir = filepath.Join(dir, "data")

	srv := New(cfg)
	if err := seedBenchmarkSandbox(srv, scale); err != nil {
		teardown()
		return nil, nil, fmt.Errorf("seed: %w", err)
	}
	return srv, teardown, nil
}

// seedBenchmarkSandbox populates the synthetic dataset:
//   - 1 bench repo                              (`bench-repo`)
//   - 5 bench tags                              (`bench-tag-a`..`e`)
//   - N bench accounts                          (`local:bench-N`)
//   - N bench subscriptions                     (one per account)
//   - rough tag distribution                    (~half tagged with -a,
//     ~third with -b, smaller fractions on c/d/e)
//
// Distribution is deterministic via a fixed-seed RNG so two runs at
// the same scale see comparable filter selectivity. Tag assignments
// hit both the sub side AND the account side so the effective-tag
// computation has both kinds of inheritance to walk.
func seedBenchmarkSandbox(srv *Server, scale int) error {
	if err := srv.git.InitBare("bench-repo"); err != nil {
		return fmt.Errorf("init bench-repo: %w", err)
	}

	// Synthetic caller name for audit-trail attribution. Shows up in
	// the temp data's audit chain but the chain is destroyed at
	// teardown so the value only matters for not-being-empty.
	const caller = "benchmark"

	tagNames := []string{"bench-tag-a", "bench-tag-b", "bench-tag-c", "bench-tag-d", "bench-tag-e"}
	for _, name := range tagNames {
		if _, err := srv.tags.Create(name, caller); err != nil {
			return fmt.Errorf("create tag %s: %w", name, err)
		}
	}

	rng := rand.New(rand.NewSource(int64(scale)))
	for i := 0; i < scale; i++ {
		identifier := fmt.Sprintf("bench-%d", i)
		if _, err := srv.accounts.Put(accounts.Account{
			Provider:    accounts.ProviderLocal,
			Identifier:  identifier,
			Role:        accounts.RoleRegular,
			DisplayName: fmt.Sprintf("Bench User %d", i),
		}); err != nil {
			return fmt.Errorf("put account %s: %w", identifier, err)
		}
		scoped := "local:" + identifier
		token, err := srv.tokenStrategy.Issue(scoped, "bench-repo", nil)
		if err != nil {
			return fmt.Errorf("issue token for %s: %w", identifier, err)
		}
		// Sub-side tag distribution: probabilities 0.5 / 0.3 / 0.15 /
		// 0.07 / 0.03 — produces realistic chip-filter selectivity
		// where the most popular tag matches half the rows and the
		// rarest matches a handful.
		var subTags []string
		probs := []float64{0.5, 0.3, 0.15, 0.07, 0.03}
		for ti, p := range probs {
			if rng.Float64() < p {
				subTags = append(subTags, tagNames[ti])
			}
		}
		if len(subTags) > 0 {
			if _, err := srv.tags.SetSubscriptionTags(token, subTags, caller); err != nil {
				return fmt.Errorf("tag sub %s: %w", token, err)
			}
		}
		// Account-side tag every 7th account with bench-tag-c so the
		// effective-tag computation has a meaningful inherited slice
		// to walk for some rows.
		if i%7 == 0 {
			if _, err := srv.tags.SetAccountTags(scoped, []string{"bench-tag-c"}, caller); err != nil {
				return fmt.Errorf("tag account %s: %w", scoped, err)
			}
		}
	}
	return nil
}

// runBenchmark executes the requested topics against the sandbox and
// aggregates per-topic timings. Sequential mode runs topics in
// caller-specified order; concurrent mode launches one goroutine per
// topic and waits for all to finish (per-topic iterations stay
// sequential within the goroutine — concurrency is *across* topics).
//
// Honours `ctx` cancellation: when the client aborts (Cancel button
// → fetch's AbortController), the request context is Done and the
// per-topic loops exit early at the next iteration boundary. Partial
// per-topic results are still appended to the slice but won't be
// written back to the wire — the handler skips the response on
// cancel — so the contract is "cancellation stops work; clients
// don't see partial results."
func runBenchmark(ctx context.Context, sandbox *Server, topics []string, iterations int, mode string) []BenchmarkTopicResult {
	results := make([]BenchmarkTopicResult, len(topics))

	if mode == "concurrent" {
		var wg sync.WaitGroup
		for i, name := range topics {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				results[i] = runOneTopic(ctx, sandbox, name, iterations)
			}(i, name)
		}
		wg.Wait()
		return results
	}

	for i, name := range topics {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			break
		}
		results[i] = runOneTopic(ctx, sandbox, name, iterations)
	}
	return results
}

// runOneTopic times `iterations` calls of one topic and returns the
// summary. Times are recorded per-iteration; stats are computed once
// at the end against the sorted slice. Slice allocation per topic is
// O(iterations) which is fine for the bounds we accept (max 10K).
//
// Checks `ctx.Err()` between iterations so a Cancel from the client
// short-circuits the loop. The percentile math is run against
// whatever samples were collected before cancellation; the handler
// discards the partial body anyway, so this is just bookkeeping
// hygiene (no NaN math against a zero-length slice).
func runOneTopic(ctx context.Context, sandbox *Server, name string, iterations int) BenchmarkTopicResult {
	fn := benchmarkTopics[name]
	samples := make([]float64, 0, iterations)
	totalStart := time.Now()
	for i := 0; i < iterations; i++ {
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		fn(sandbox)
		samples = append(samples, float64(time.Since(start).Microseconds())/1000.0)
	}
	totalMs := float64(time.Since(totalStart).Microseconds()) / 1000.0

	sort.Float64s(samples)
	return BenchmarkTopicResult{
		Topic:      name,
		Iterations: len(samples),
		MedianMs:   percentile(samples, 50),
		P95Ms:      percentile(samples, 95),
		P99Ms:      percentile(samples, 99),
		TotalMs:    totalMs,
	}
}

// percentile reads the p-th percentile off a SORTED slice. Empty
// slice → 0. Index uses the nearest-rank method (no interpolation):
// at ~10K-sample scale the interpolation difference is below a
// microsecond, well under the noise floor, so simpler is fine.
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
