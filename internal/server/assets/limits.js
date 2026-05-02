// /admin/limits — operator tunables. Two number inputs (push slots
// + retry-after seconds) backed by GET/PATCH /api/admin/limits.
// Save is enabled only when the form is dirty; success applies +
// persists in one round-trip.

(function () {
  const Admin = window.Admin;

  // pristine holds the values last loaded from the server, so the
  // dirty check has something to compare against. Save only commits
  // fields that actually changed (PATCH body skips unchanged keys),
  // matching the partial-update contract of the endpoint.
  let pristine = { push_slots: null, push_retry_after_sec: null };

  function readInputs() {
    return {
      push_slots:            parseInt(document.getElementById('push-slots').value, 10),
      push_retry_after_sec:  parseInt(document.getElementById('push-retry-after-sec').value, 10),
    };
  }

  function setInputs(payload) {
    if (payload.push_slots != null) {
      document.getElementById('push-slots').value = payload.push_slots;
    }
    if (payload.push_retry_after_sec != null) {
      document.getElementById('push-retry-after-sec').value = payload.push_retry_after_sec;
    }
    pristine = {
      push_slots:           payload.push_slots,
      push_retry_after_sec: payload.push_retry_after_sec,
    };
    refreshState(payload);
    refreshDirty();
  }

  // refreshState paints the live "currently 3 / 10 in use" line off
  // the GET / PATCH response. Updates after every Save so the admin
  // can see the resize landed.
  function refreshState(payload) {
    const el = document.getElementById('limits-state');
    if (!el) return;
    const inUse = payload.push_slot_in_use != null ? payload.push_slot_in_use : 0;
    const cap   = payload.push_slots != null ? payload.push_slots : 0;
    el.textContent = 'Currently ' + inUse + ' / ' + cap + ' push slots in use.';
  }

  function refreshDirty() {
    const cur = readInputs();
    const dirty =
      cur.push_slots !== pristine.push_slots ||
      cur.push_retry_after_sec !== pristine.push_retry_after_sec;
    document.getElementById('limits-save-btn').disabled = !dirty;
  }

  function setMsg(text, kind) {
    const el = document.getElementById('limits-msg');
    if (!el) return;
    el.textContent = text || '';
    el.className = 'limits-msg ' + (kind === 'error' ? 'error' : 'muted');
  }

  async function load() {
    setMsg('');
    try {
      const r = await fetch('/api/admin/limits', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('GET failed: HTTP ' + r.status);
      setInputs(await r.json());
    } catch (e) {
      setMsg(e.message || String(e), 'error');
    }
  }

  // save sends only the fields that changed, mirroring the
  // partial-update contract. If both fields differ from pristine
  // both go in the body; otherwise just the ones that moved.
  async function save() {
    setMsg('saving...');
    const cur = readInputs();
    const body = {};
    if (cur.push_slots !== pristine.push_slots) {
      body.push_slots = cur.push_slots;
    }
    if (cur.push_retry_after_sec !== pristine.push_retry_after_sec) {
      body.push_retry_after_sec = cur.push_retry_after_sec;
    }
    if (Object.keys(body).length === 0) {
      setMsg('');
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
      setInputs(await r.json());
      setMsg('saved');
    } catch (e) {
      setMsg(e.message || String(e), 'error');
    }
  }

  (async function boot() {
    if (!(await Admin.bootPage('limits'))) return;
    document.getElementById('push-slots').addEventListener('input', refreshDirty);
    document.getElementById('push-retry-after-sec').addEventListener('input', refreshDirty);
    document.getElementById('limits-save-btn').addEventListener('click', save);
    await load();
  })();
})();
