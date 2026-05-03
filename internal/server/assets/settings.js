// /admin/settings — operator tunables. Two independent cards, each
// with its own Save button + dirty check, backed by:
//   - GET/PATCH /api/admin/limits  (push concurrency)
//   - GET/PATCH /api/admin/mirror  (remote-status polling)
// Each section is its own load + save pair so a Save on one card
// does NOT submit the other card's changes; that keeps "I'm only
// twiddling concurrency" from accidentally writing a stale poll
// value to disk.

(function () {
  const Admin = window.Admin;

  // -- Push concurrency (limits) -----------------------------------

  let limitsPristine = { push_slots: null, push_retry_after_sec: null };

  function readLimitsInputs() {
    return {
      push_slots:           parseInt(document.getElementById('push-slots').value, 10),
      push_retry_after_sec: parseInt(document.getElementById('push-retry-after-sec').value, 10),
    };
  }

  function setLimitsInputs(payload) {
    if (payload.push_slots != null) {
      document.getElementById('push-slots').value = payload.push_slots;
    }
    if (payload.push_retry_after_sec != null) {
      document.getElementById('push-retry-after-sec').value = payload.push_retry_after_sec;
    }
    limitsPristine = {
      push_slots:           payload.push_slots,
      push_retry_after_sec: payload.push_retry_after_sec,
    };
    refreshLimitsState(payload);
    refreshLimitsDirty();
  }

  function refreshLimitsState(payload) {
    const el = document.getElementById('limits-state');
    if (!el) return;
    const inUse = payload.push_slot_in_use != null ? payload.push_slot_in_use : 0;
    const cap   = payload.push_slots != null ? payload.push_slots : 0;
    el.textContent = 'Currently ' + inUse + ' / ' + cap + ' push slots in use.';
  }

  function refreshLimitsDirty() {
    const cur = readLimitsInputs();
    const dirty =
      cur.push_slots !== limitsPristine.push_slots ||
      cur.push_retry_after_sec !== limitsPristine.push_retry_after_sec;
    document.getElementById('limits-save-btn').disabled = !dirty;
  }

  function setLimitsMsg(text, kind) {
    const el = document.getElementById('limits-msg');
    if (!el) return;
    el.textContent = text || '';
    el.className = 'limits-msg ' + (kind === 'error' ? 'error' : 'muted');
  }

  async function loadLimits() {
    setLimitsMsg('');
    try {
      const r = await fetch('/api/admin/limits', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('GET failed: HTTP ' + r.status);
      setLimitsInputs(await r.json());
    } catch (e) {
      setLimitsMsg(e.message || String(e), 'error');
    }
  }

  async function saveLimits() {
    setLimitsMsg('saving...');
    const cur = readLimitsInputs();
    const body = {};
    if (cur.push_slots !== limitsPristine.push_slots) {
      body.push_slots = cur.push_slots;
    }
    if (cur.push_retry_after_sec !== limitsPristine.push_retry_after_sec) {
      body.push_retry_after_sec = cur.push_retry_after_sec;
    }
    if (Object.keys(body).length === 0) {
      setLimitsMsg('');
      return;
    }
    try {
      const r = await fetch('/api/admin/limits', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) {
        let msg = 'PATCH failed: HTTP ' + r.status;
        try { msg = (await r.json()).error || msg; } catch { /* keep default */ }
        throw new Error(msg);
      }
      setLimitsInputs(await r.json());
      setLimitsMsg('saved');
    } catch (e) {
      setLimitsMsg(e.message || String(e), 'error');
    }
  }

  // -- Mirror remote-status polling --------------------------------
  //
  // Two controls (enabled toggle + cadence input) map to one server
  // value (status_poll_sec). The mapping is:
  //   - toggle ON, cadence N → status_poll_sec=N
  //   - toggle OFF           → status_poll_sec=0 (cadence preserved
  //                            client-side in the input so flipping
  //                            back ON keeps the user's number)
  //
  // pristine tracks the LAST APPLIED state so the dirty check fires
  // when EITHER control diverges. Heartbeat (last_tick_at + friends)
  // lives on the same response and is read-only.

  let mirrorPristine = { enabled: null, cadence: null };
  // mirrorPollAge is the timer that re-fetches the heartbeat while
  // the page is open. Cleared on unload so a backgrounded tab
  // doesn't keep hitting /api/admin/mirror forever.
  let mirrorPollAge = null;
  // mirrorIntervalSec: the most recent server-reported cadence.
  // Used to decide whether the heartbeat is stale (>2x interval).
  let mirrorIntervalSec = 0;

  function readMirrorForm() {
    const enabledEl = document.querySelector('#mirror-enabled-host input[type="checkbox"]');
    const cadEl = document.getElementById('mirror-status-poll-sec');
    return {
      enabled: !!(enabledEl && enabledEl.checked),
      cadence: parseInt(cadEl.value, 10) || 0,
    };
  }

  // mirrorBodyForSave maps the two-control form back to the server
  // value. Toggle off zeroes out the cadence; toggle on sends
  // whatever the input shows.
  function mirrorBodyForSave(form) {
    return { status_poll_sec: form.enabled ? form.cadence : 0 };
  }

  function setMirrorEnabled(checked) {
    const cadEl = document.getElementById('mirror-status-poll-sec');
    if (cadEl) cadEl.disabled = !checked;
    refreshMirrorDirty();
  }

  // setMirrorInputs writes server response into the form + pristine.
  // First call wires the toggle; subsequent calls just update state.
  function setMirrorInputs(payload) {
    const cadEl = document.getElementById('mirror-status-poll-sec');
    const sec = payload.status_poll_sec != null ? payload.status_poll_sec : 0;
    const enabled = !!payload.enabled;
    mirrorIntervalSec = sec > 0 ? sec : mirrorIntervalSec;

    // Cadence: when on, mirror what the server stored. When off,
    // leave whatever the user has typed (or fall back to 600).
    if (sec > 0) {
      cadEl.value = sec;
    } else if (!cadEl.value || parseInt(cadEl.value, 10) <= 0) {
      cadEl.value = 600;
    }
    cadEl.disabled = !enabled;

    // Toggle: set checked state on the underlying checkbox without
    // firing the change handler (which would mark form dirty).
    const switchEl = document.querySelector('#mirror-enabled-host input[type="checkbox"]');
    if (switchEl) switchEl.checked = enabled;

    mirrorPristine = { enabled, cadence: parseInt(cadEl.value, 10) };
    refreshMirrorState(payload);
    refreshMirrorHeartbeat(payload);
    refreshMirrorDirty();
  }

  // refreshMirrorState paints the live "checking N destinations" line.
  function refreshMirrorState(payload) {
    const el = document.getElementById('mirror-state');
    if (!el) return;
    const n = payload.enabled_destinations != null ? payload.enabled_destinations : 0;
    const enabled = !!payload.enabled;
    const sec = payload.status_poll_sec != null ? payload.status_poll_sec : 0;
    if (!enabled) {
      el.textContent = 'Polling disabled. ' + n + ' enabled destination(s); manual refresh only.';
    } else {
      el.textContent = 'Checking ' + n + ' enabled destination(s) every ' + sec + ' s.';
    }
  }

  // refreshMirrorHeartbeat renders the "Last poll: 2m 13s ago" line
  // and flips it to a warning class if the timestamp is older than
  // 2x the interval. Empty when polling is disabled or no tick has
  // happened yet (the line collapses; the state line above carries
  // "polling disabled" already).
  function refreshMirrorHeartbeat(payload) {
    const el = document.getElementById('mirror-heartbeat');
    if (!el) return;
    const lastAt = payload.last_tick_at;
    const lastErr = payload.last_tick_error || '';
    if (!payload.enabled) {
      el.textContent = '';
      el.className = 'limits-state mirror-heartbeat';
      return;
    }
    if (!lastAt) {
      el.textContent = 'No poll has run yet (next within ' + (payload.status_poll_sec || 0) + ' s).';
      el.className = 'limits-state mirror-heartbeat muted';
      return;
    }
    const ageMs = Date.now() - new Date(lastAt).getTime();
    const stale = mirrorIntervalSec > 0 && ageMs > mirrorIntervalSec * 1000 * 2;
    let line = 'Last poll: ' + formatAge(ageMs) + ' ago';
    if (payload.last_tick_duration_ms) {
      line += ' (took ' + payload.last_tick_duration_ms + ' ms)';
    }
    if (lastErr) {
      line += '. Last error: ' + lastErr;
    }
    el.textContent = line;
    el.className = 'limits-state mirror-heartbeat ' + (stale || lastErr ? 'error' : 'muted');
  }

  // formatAge — short human form, no trailing zeros.
  function formatAge(ms) {
    const s = Math.floor(ms / 1000);
    if (s < 60) return s + 's';
    const m = Math.floor(s / 60);
    const sr = s % 60;
    if (m < 60) return sr ? m + 'm ' + sr + 's' : m + 'm';
    const h = Math.floor(m / 60);
    const mr = m % 60;
    return mr ? h + 'h ' + mr + 'm' : h + 'h';
  }

  function refreshMirrorDirty() {
    const cur = readMirrorForm();
    const dirty = cur.enabled !== mirrorPristine.enabled ||
                  (cur.enabled && cur.cadence !== mirrorPristine.cadence);
    document.getElementById('mirror-save-btn').disabled = !dirty;
  }

  function setMirrorMsg(text, kind) {
    const el = document.getElementById('mirror-msg');
    if (!el) return;
    el.textContent = text || '';
    el.className = 'limits-msg ' + (kind === 'error' ? 'error' : 'muted');
  }

  async function loadMirror() {
    setMirrorMsg('');
    try {
      const r = await fetch('/api/admin/mirror', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('GET failed: HTTP ' + r.status);
      setMirrorInputs(await r.json());
    } catch (e) {
      setMirrorMsg(e.message || String(e), 'error');
    }
  }

  async function saveMirror() {
    setMirrorMsg('saving...');
    const cur = readMirrorForm();
    if (cur.enabled === mirrorPristine.enabled &&
        cur.cadence === mirrorPristine.cadence) {
      setMirrorMsg('');
      return;
    }
    try {
      const r = await fetch('/api/admin/mirror', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(mirrorBodyForSave(cur)),
      });
      if (!r.ok) {
        let msg = 'PATCH failed: HTTP ' + r.status;
        try { msg = (await r.json()).error || msg; } catch { /* keep default */ }
        throw new Error(msg);
      }
      setMirrorInputs(await r.json());
      setMirrorMsg('saved');
    } catch (e) {
      setMirrorMsg(e.message || String(e), 'error');
    }
  }

  // Heartbeat re-fetch only — leaves the form values alone (so a
  // user who's edited the cadence input mid-session doesn't have
  // their unsaved change overwritten by an auto-poll). Renders just
  // the heartbeat + state lines.
  async function refreshMirrorHeartbeatTick() {
    try {
      const r = await fetch('/api/admin/mirror', { credentials: 'same-origin' });
      if (!r.ok) return;
      const payload = await r.json();
      mirrorIntervalSec = payload.status_poll_sec || mirrorIntervalSec;
      refreshMirrorState(payload);
      refreshMirrorHeartbeat(payload);
    } catch { /* silent — next tick retries */ }
  }

  (async function boot() {
    if (!(await Admin.bootPage('settings'))) return;
    document.getElementById('push-slots').addEventListener('input', refreshLimitsDirty);
    document.getElementById('push-retry-after-sec').addEventListener('input', refreshLimitsDirty);
    document.getElementById('limits-save-btn').addEventListener('click', saveLimits);

    // Mount the mirror toggle. GG.toggle_switch.html renders the
    // checkbox + label markup; we wire onChange to refresh the
    // dirty check + grey/ungrey the cadence input. The existing
    // shape on the destinations card is the precedent.
    const switchHost = document.getElementById('mirror-enabled-host');
    if (switchHost) {
      switchHost.innerHTML = GG.toggle_switch.html({
        checked: true,
        ariaLabel: 'Background polling enabled',
      });
      const switchEl = switchHost.querySelector('input[type="checkbox"]');
      GG.toggle_switch.onChange(switchEl, (checked) => setMirrorEnabled(checked));
    }
    document.getElementById('mirror-status-poll-sec').addEventListener('input', refreshMirrorDirty);
    document.getElementById('mirror-save-btn').addEventListener('click', saveMirror);

    await Promise.all([loadLimits(), loadMirror()]);

    // Auto-refresh the heartbeat every 30 s so an admin watching
    // the page sees ticks land in real time.
    mirrorPollAge = setInterval(refreshMirrorHeartbeatTick, 30000);
    window.addEventListener('beforeunload', () => {
      if (mirrorPollAge) clearInterval(mirrorPollAge);
    });
  })();
})();
