// /admin/benchmark — admin-only synthetic-sandbox micro-benchmark UI.
// Reads four inputs (scale, mode, topics, iterations), POSTs to
// /api/admin/benchmark, renders results.
//
// The page itself is config; the runner lives entirely on the server.
// Each click of "Run benchmark" creates and tears down a fresh
// sandbox in-process, so the cost the user sees is one round-trip
// plus the server-side measurement loop. Concurrent mode parallelises
// across topics on the server side, not the client.

(function () {
  const Admin = window.Admin;

  // currentRun holds the AbortController for the in-flight benchmark
  // (null when nothing is running). Cancel button calls .abort() on
  // it, which closes the fetch and trips the server-side
  // r.Context().Done() so the runner exits its iteration loops at
  // the next boundary.
  let currentRun = null;

  // setRunning swaps Run / Cancel button visibility. Run is hidden
  // and Cancel is shown while a benchmark is in flight; the inverse
  // when idle. Disabling the toggles + iterations input keeps the
  // operator from changing settings mid-run (which would have no
  // effect on the already-launched request anyway, but the UI lying
  // about what's pending is worse than a few greyed-out controls).
  function setRunning(running) {
    document.getElementById('bench-run-btn').classList.toggle('hidden', running);
    document.getElementById('bench-cancel-btn').classList.toggle('hidden', !running);
    document.querySelectorAll(
      'input[name="bench-scale"], input[name="bench-mode"], input[name="bench-topic"], #bench-iterations',
    ).forEach(el => { el.disabled = running; });
  }

  function selectedTopics() {
    return Array.from(
      document.querySelectorAll('input[name="bench-topic"]:checked'),
    ).map(el => el.value);
  }

  function selectedScale() {
    const el = document.querySelector('input[name="bench-scale"]:checked');
    return el ? parseInt(el.value, 10) : 0;
  }

  function selectedMode() {
    const el = document.querySelector('input[name="bench-mode"]:checked');
    return el ? el.value : 'sequential';
  }

  function readIterations() {
    const el = document.getElementById('bench-iterations');
    const n = parseInt((el && el.value) || '0', 10);
    return Number.isFinite(n) ? n : 0;
  }

  // setStatus paints the small text next to the Run button so the
  // operator gets feedback during long runs (1000 subs × 100 iter ×
  // 6 topics easily costs several seconds, and the request is
  // synchronous on the server so the spinner-equivalent matters).
  function setStatus(text, kind) {
    const el = document.getElementById('bench-status');
    if (!el) return;
    el.textContent = text || '';
    el.className = 'bench-status ' + (kind === 'error' ? 'error' : 'muted');
  }

  // formatMs trims to a fixed-width display. Sub-millisecond values
  // get three decimals so the operator can still distinguish 0.012
  // from 0.087; ≥1 ms gets two so the columns line up.
  function formatMs(v) {
    if (v == null || isNaN(v)) return '-';
    if (v < 1) return v.toFixed(3);
    if (v < 100) return v.toFixed(2);
    return v.toFixed(1);
  }

  // renderResults paints the result table, sorted by p95 descending
  // so the slowest topics float to the top — that's where an
  // operator's eye should land first when deciding whether the
  // hardware is keeping up. Inline horizontal bar per row gives an
  // at-a-glance read; bar width is normalised against the worst
  // p95 in the response.
  function renderResults(payload) {
    const card = document.getElementById('bench-results-card');
    const rows = document.getElementById('bench-results-rows');
    const meta = document.getElementById('bench-meta');
    if (!card || !rows || !meta) return;

    const sorted = (payload.results || []).slice().sort((a, b) => b.p95_ms - a.p95_ms);
    const maxP95 = sorted.length ? sorted[0].p95_ms || 0.0001 : 1;

    meta.textContent =
      'Scale: ' + payload.scale + ' subs · Mode: ' + payload.mode +
      ' · Iterations: ' + payload.iterations +
      ' · Setup: ' + formatMs(payload.setup_ms) + ' ms';

    rows.replaceChildren();
    for (const r of sorted) {
      const tr = document.createElement('tr');
      const pct = Math.max(2, Math.round((r.p95_ms / maxP95) * 100));
      tr.innerHTML =
        '<td><code>' + r.topic + '</code></td>' +
        '<td>' + r.iterations + '</td>' +
        '<td>' + formatMs(r.median_ms) + '</td>' +
        '<td>' + formatMs(r.p95_ms) + '</td>' +
        '<td>' + formatMs(r.p99_ms) + '</td>' +
        '<td>' + formatMs(r.total_ms) + '</td>' +
        '<td><div class="bench-bar" style="width:' + pct + '%"></div></td>';
      rows.appendChild(tr);
    }
    card.classList.remove('hidden');
  }

  async function runBenchmark() {
    const topics = selectedTopics();
    if (topics.length === 0) {
      setStatus('Pick at least one topic.', 'error');
      return;
    }
    const iterations = readIterations();
    if (iterations < 1 || iterations > 10000) {
      setStatus('Iterations must be between 1 and 10000.', 'error');
      return;
    }

    currentRun = new AbortController();
    setRunning(true);
    setStatus('running...');
    const t0 = performance.now();
    try {
      const r = await fetch('/api/admin/benchmark', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        signal: currentRun.signal,
        body: JSON.stringify({
          scale: selectedScale(),
          mode: selectedMode(),
          iterations,
          topics,
        }),
      });
      if (!r.ok) {
        let msg = 'benchmark failed: HTTP ' + r.status;
        try { msg = (await r.json()).error || msg; } catch { /* keep default */ }
        throw new Error(msg);
      }
      const payload = await r.json();
      renderResults(payload);
      const elapsed = ((performance.now() - t0) / 1000).toFixed(2);
      setStatus('done in ' + elapsed + ' s');
    } catch (e) {
      // AbortError is the user clicking Cancel — show a friendly
      // "cancelled" instead of the raw "AbortError: ..." message.
      // Other errors (network, server 4xx/5xx) surface as-is.
      if (e && e.name === 'AbortError') {
        setStatus('cancelled');
      } else {
        setStatus(e.message || String(e), 'error');
      }
    } finally {
      currentRun = null;
      setRunning(false);
    }
  }

  function cancelBenchmark() {
    if (currentRun) currentRun.abort();
  }

  (async function boot() {
    if (!(await Admin.bootPage('benchmark'))) return;
    const runBtn = document.getElementById('bench-run-btn');
    if (runBtn) runBtn.addEventListener('click', runBenchmark);
    const cancelBtn = document.getElementById('bench-cancel-btn');
    if (cancelBtn) cancelBtn.addEventListener('click', cancelBenchmark);
  })();
})();
