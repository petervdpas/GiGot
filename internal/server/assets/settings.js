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

  let mirrorPristine = { status_poll_sec: null };

  function readMirrorInputs() {
    return {
      status_poll_sec: parseInt(document.getElementById('mirror-status-poll-sec').value, 10),
    };
  }

  function setMirrorInputs(payload) {
    if (payload.status_poll_sec != null) {
      document.getElementById('mirror-status-poll-sec').value = payload.status_poll_sec;
    }
    mirrorPristine = { status_poll_sec: payload.status_poll_sec };
    refreshMirrorState(payload);
    refreshMirrorDirty();
  }

  // refreshMirrorState paints the live "checking N enabled
  // destinations" line. When polling is disabled (0), the message
  // shifts to call that out so an admin doesn't think a stuck
  // counter means a hung poller.
  function refreshMirrorState(payload) {
    const el = document.getElementById('mirror-state');
    if (!el) return;
    const n = payload.enabled_destinations != null ? payload.enabled_destinations : 0;
    const sec = payload.status_poll_sec != null ? payload.status_poll_sec : 0;
    if (sec <= 0) {
      el.textContent = 'Polling disabled. ' + n + ' enabled destination(s); manual refresh only.';
    } else {
      el.textContent = 'Checking ' + n + ' enabled destination(s) every ' + sec + ' s.';
    }
  }

  function refreshMirrorDirty() {
    const cur = readMirrorInputs();
    const dirty = cur.status_poll_sec !== mirrorPristine.status_poll_sec;
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
    const cur = readMirrorInputs();
    if (cur.status_poll_sec === mirrorPristine.status_poll_sec) {
      setMirrorMsg('');
      return;
    }
    try {
      const r = await fetch('/api/admin/mirror', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ status_poll_sec: cur.status_poll_sec }),
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

  (async function boot() {
    if (!(await Admin.bootPage('settings'))) return;
    document.getElementById('push-slots').addEventListener('input', refreshLimitsDirty);
    document.getElementById('push-retry-after-sec').addEventListener('input', refreshLimitsDirty);
    document.getElementById('limits-save-btn').addEventListener('click', saveLimits);
    document.getElementById('mirror-status-poll-sec').addEventListener('input', refreshMirrorDirty);
    document.getElementById('mirror-save-btn').addEventListener('click', saveMirror);
    await Promise.all([loadLimits(), loadMirror()]);
  })();
})();
