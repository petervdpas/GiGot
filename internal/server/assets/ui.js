// Shared UI primitives for the admin pages. One global `GG` namespace
// with four modules, ported from ~/Projects/goop2/internal/ui/assets/js:
//
//   GG.core          — escapeHtml, qs, onReady, localStorage safe wrappers.
//   GG.toggle_switch — <label class="switch"> markup + init/val/onChange.
//   GG.select        — fully custom .gsel dropdown (replaces <select>).
//   GG.theme         — dark/light theme toggle persisted to localStorage.
//
// Consumed by admin.js and credentials.js. Both pages include this file
// before their page-specific script so globals are in place on init.
//
// Kept intentionally framework-free — same rationale as the rest of the
// admin UI: no bundler, no build step, ships as a single embedded asset.

(() => {
  window.GG = window.GG || {};

  // ---------------------------------------------------------------- core
  (function () {
    function escapeHtml(s) {
      return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
      }[c]));
    }
    function qs(sel, root) { return (root || document).querySelector(sel); }
    function qsa(sel, root) { return Array.from((root || document).querySelectorAll(sel)); }
    function onReady(fn) {
      if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', fn, { once: true });
      } else {
        fn();
      }
    }
    function safeLocalStorageGet(key) {
      try { return localStorage.getItem(key); } catch { return null; }
    }
    function safeLocalStorageSet(key, val) {
      try { localStorage.setItem(key, val); } catch { /* quota / disabled */ }
    }
    GG.core = { escapeHtml, qs, qsa, onReady, safeLocalStorageGet, safeLocalStorageSet };
  })();

  // ------------------------------------------------------- toggle_switch
  // <label class="switch"><input type=checkbox><span class="slider"></span></label>
  // Optional trailing "<span class="control-label">…</span>" when opts.label is set.
  (function () {
    const esc = GG.core.escapeHtml;

    function html(opts) {
      opts = opts || {};
      const id = opts.id ? ' id="' + esc(opts.id) + '"' : '';
      const name = opts.name ? ' name="' + esc(opts.name) + '"' : '';
      const value = opts.value != null ? ' value="' + esc(opts.value) + '"' : '';
      const checked = opts.checked ? ' checked' : '';
      const required = opts.required ? ' required' : '';
      const title = opts.title ? ' title="' + esc(opts.title) + '"' : '';
      const aria = opts.ariaLabel ? ' aria-label="' + esc(opts.ariaLabel) + '"' : '';
      const extra = opts.className ? ' ' + opts.className : '';
      let h = '<label class="switch' + extra + '"' + title + '>' +
        '<input type="checkbox"' + id + name + value + checked + required + aria + '>' +
        '<span class="slider"></span>' +
        '</label>';
      if (opts.label) {
        h += '<span class="control-label">' + esc(opts.label) + '</span>';
      }
      return h;
    }

    function resolve(idOrEl) {
      const el = typeof idOrEl === 'string' ? GG.core.qs('#' + idOrEl) : idOrEl;
      if (!el) return null;
      if (el.type === 'checkbox') return el;
      return el.querySelector('input[type="checkbox"]');
    }
    function val(idOrEl) { const cb = resolve(idOrEl); return cb ? cb.checked : false; }
    function setVal(idOrEl, checked) { const cb = resolve(idOrEl); if (cb) cb.checked = !!checked; }
    function onChange(idOrEl, fn) {
      const cb = resolve(idOrEl);
      if (cb) cb.addEventListener('change', () => fn(cb.checked));
    }
    GG.toggle_switch = { html, val, setVal, onChange };
  })();

  // ------------------------------------------------------------- select
  // Fully custom .gsel dropdown. Renders a hidden <input> when opts.name
  // is set so the element round-trips through a plain HTML form. Port of
  // goop2's Goop.select — same markup and behaviour, trimmed of the
  // grouped-options path we don't need in GiGot today.
  (function () {
    const esc = GG.core.escapeHtml;

    function html(opts) {
      opts = opts || {};
      const id = opts.id ? ' id="' + esc(opts.id) + '"' : '';
      const cls = 'gsel' + (opts.className ? ' ' + opts.className : '');
      const val = opts.value || '';
      const placeholder = opts.placeholder || '';
      const options = opts.options || [];

      let selLabel = placeholder;
      for (const o of options) { if (o.value === val) selLabel = o.label; }

      let h = '<div' + id + ' class="' + cls + '"' +
        ' data-value="' + esc(val) + '"' +
        ' data-placeholder="' + esc(placeholder) + '">';
      if (opts.name) {
        h += '<input type="hidden" name="' + esc(opts.name) + '" value="' + esc(val) + '">';
      }
      h += '<button type="button" class="gsel-trigger">' +
        '<span class="gsel-text">' + esc(selLabel) + '</span>' +
        '<span class="gsel-arrow">&#9662;</span>' +
        '</button><div class="gsel-dropdown">';
      for (const o of options) {
        const sel = o.value === val ? ' selected' : '';
        const dis = o.disabled ? ' disabled' : '';
        h += '<button type="button" class="gsel-option' + sel + '"' + dis +
          ' data-value="' + esc(o.value) + '">' + esc(o.label) + '</button>';
      }
      h += '</div></div>';
      return h;
    }

    function fitDropdown(el) {
      const dd = el.querySelector('.gsel-dropdown');
      if (!dd) return;
      dd.style.maxHeight = '';
      const rect = dd.getBoundingClientRect();
      const space = window.innerHeight - rect.top - 8;
      if (space < rect.height && space > 60) dd.style.maxHeight = space + 'px';
    }

    function applyVal(el, value, label) {
      el.setAttribute('data-value', value);
      const textEl = el.querySelector('.gsel-text');
      if (textEl && label != null) textEl.textContent = label;
      const hidden = el.querySelector('input[type="hidden"]');
      if (hidden) hidden.value = value;
    }

    function init(el, onChange) {
      if (!el) return;
      if (onChange) el._gselChange = onChange;
      if (el._gselInit) return;
      el._gselInit = true;

      const trigger = el.querySelector('.gsel-trigger');
      if (!trigger) return;
      trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        document.querySelectorAll('.gsel.open').forEach(other => {
          if (other !== el) other.classList.remove('open');
        });
        el.classList.toggle('open');
        if (el.classList.contains('open')) fitDropdown(el);
      });
      el.addEventListener('click', (e) => {
        const opt = e.target.closest ? e.target.closest('.gsel-option') : null;
        if (!opt || opt.disabled) return;
        e.stopPropagation();
        const newVal = opt.getAttribute('data-value');
        applyVal(el, newVal, opt.textContent);
        el.querySelectorAll('.gsel-option').forEach(o => o.classList.toggle('selected', o === opt));
        el.classList.remove('open');
        if (el._gselChange) el._gselChange(newVal);
      });
      document.addEventListener('click', () => el.classList.remove('open'));
    }

    function val(el) { return el ? (el.getAttribute('data-value') || '') : ''; }

    function setVal(el, value) {
      if (!el) return;
      const opt = el.querySelector('.gsel-option[data-value="' + CSS.escape(value) + '"]');
      const label = opt ? opt.textContent : (el.getAttribute('data-placeholder') || '');
      applyVal(el, value, label);
      el.querySelectorAll('.gsel-option').forEach(o => o.classList.toggle('selected', o.getAttribute('data-value') === value));
    }

    // Scan a container (or document) and bind every un-initialised .gsel.
    // Callers render `.gsel` blobs into the DOM, then call initAll on the
    // surrounding region to attach click behaviour — same idea as goop2's
    // autoInit, but explicit so dynamic re-renders can re-bind reliably.
    function initAll(container) {
      (container || document).querySelectorAll('.gsel').forEach(el => init(el));
    }

    GG.select = { html, init, initAll, val, setVal };
  })();

  // --------------------------------------------------------- datepicker
  // Custom date picker port of goop2's Goop.datepicker. We use it
  // instead of <input type="date"> because Firefox on Linux renders
  // the native calendar popup with OS-theme chrome that ignores our
  // color-scheme and CSS variables entirely. A custom picker is the
  // only way to keep it on-palette in dark mode.
  //
  // Usage: `<input type="text" data-gdp placeholder="YYYY-MM-DD">` in
  // markup, then GG.datepicker.initAll(container) on boot. On pick,
  // input.value becomes the ISO date "YYYY-MM-DD" — same format native
  // <input type="date"> would have given us, so the caller's submit
  // code doesn't need to care which kind of picker was used.
  (function () {
    const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June',
      'July', 'August', 'September', 'October', 'November', 'December'];
    let activePopup = null;

    function pad(n) { return n < 10 ? '0' + n : '' + n; }
    function formatDate(y, m, d) { return y + '-' + pad(m + 1) + '-' + pad(d); }
    function parseDate(s) {
      if (!s) return null;
      const parts = s.split('-');
      if (parts.length !== 3) return null;
      return { year: +parts[0], month: +parts[1] - 1, day: +parts[2] };
    }
    function daysInMonth(y, m) { return new Date(y, m + 1, 0).getDate(); }
    function firstDayOfWeek(y, m) { return new Date(y, m, 1).getDay(); }

    function closePopup() { if (activePopup) { activePopup.remove(); activePopup = null; } }

    function buildCalendar(input, year, month) {
      closePopup();
      const selected = parseDate(input.value);
      const today = new Date();
      const todayStr = formatDate(today.getFullYear(), today.getMonth(), today.getDate());

      const popup = document.createElement('div');
      popup.className = 'gdp-popup';
      activePopup = popup;

      let header = '<div class="gdp-header">' +
        '<button type="button" class="gdp-nav" data-dir="-1" data-scope="month">&#8249;</button>' +
        '<span class="gdp-month">' + MONTHS[month] + '</span>' +
        '<button type="button" class="gdp-nav" data-dir="1" data-scope="month">&#8250;</button>' +
        '<button type="button" class="gdp-nav" data-dir="-1" data-scope="year">&#8249;</button>' +
        '<span class="gdp-year">' + year + '</span>' +
        '<button type="button" class="gdp-nav" data-dir="1" data-scope="year">&#8250;</button>' +
        '</div>';
      let dayHeaders = '<div class="gdp-days">';
      DAYS.forEach(d => { dayHeaders += '<span class="gdp-day-name">' + d + '</span>'; });
      dayHeaders += '</div>';

      let grid = '<div class="gdp-grid">';
      const dim = daysInMonth(year, month);
      const start = firstDayOfWeek(year, month);
      const prevDim = daysInMonth(year, month === 0 ? 11 : month - 1);
      for (let i = start - 1; i >= 0; i--) {
        grid += '<span class="gdp-cell gdp-other">' + (prevDim - i) + '</span>';
      }
      for (let d = 1; d <= dim; d++) {
        const dateStr = formatDate(year, month, d);
        let cls = 'gdp-cell';
        if (selected && selected.year === year && selected.month === month && selected.day === d) cls += ' gdp-selected';
        if (dateStr === todayStr) cls += ' gdp-today';
        grid += '<span class="' + cls + '" data-date="' + dateStr + '">' + d + '</span>';
      }
      const totalCells = start + dim;
      const remaining = (7 - (totalCells % 7)) % 7;
      for (let r = 1; r <= remaining; r++) {
        grid += '<span class="gdp-cell gdp-other">' + r + '</span>';
      }
      grid += '</div>';

      const footer = '<div class="gdp-footer">' +
        '<button type="button" class="gdp-today-btn">Today</button>' +
        '<button type="button" class="gdp-clear-btn">Clear</button>' +
        '</div>';

      popup.innerHTML = header + dayHeaders + grid + footer;

      const rect = input.getBoundingClientRect();
      popup.style.top = (rect.bottom + window.scrollY + 4) + 'px';
      popup.style.left = (rect.left + window.scrollX) + 'px';
      document.body.appendChild(popup);

      const popupRect = popup.getBoundingClientRect();
      if (popupRect.right > window.innerWidth - 8) {
        popup.style.left = (rect.right + window.scrollX - popupRect.width) + 'px';
      }

      popup.querySelectorAll('.gdp-nav').forEach(btn => {
        btn.addEventListener('click', e => {
          e.preventDefault(); e.stopPropagation();
          const dir = parseInt(btn.dataset.dir, 10);
          const scope = btn.dataset.scope;
          if (scope === 'year') return buildCalendar(input, year + dir, month);
          let nm = month + dir, ny = year;
          if (nm < 0) { nm = 11; ny--; }
          if (nm > 11) { nm = 0; ny++; }
          buildCalendar(input, ny, nm);
        });
      });
      popup.querySelectorAll('.gdp-cell[data-date]').forEach(cell => {
        cell.addEventListener('click', e => {
          e.preventDefault(); e.stopPropagation();
          input.value = cell.dataset.date;
          input.dispatchEvent(new Event('change', { bubbles: true }));
          closePopup();
        });
      });
      popup.querySelector('.gdp-today-btn').addEventListener('click', e => {
        e.preventDefault(); e.stopPropagation();
        input.value = todayStr;
        input.dispatchEvent(new Event('change', { bubbles: true }));
        closePopup();
      });
      popup.querySelector('.gdp-clear-btn').addEventListener('click', e => {
        e.preventDefault(); e.stopPropagation();
        input.value = '';
        input.dispatchEvent(new Event('change', { bubbles: true }));
        closePopup();
      });
    }

    function attach(input) {
      if (!input || input.dataset.gdpBound) return;
      input.dataset.gdpBound = '1';
      input.setAttribute('readonly', '');
      input.type = 'text';
      input.style.cursor = 'pointer';
      input.addEventListener('click', e => {
        e.stopPropagation();
        if (activePopup) { closePopup(); return; }
        const parsed = parseDate(input.value);
        const now = new Date();
        buildCalendar(input,
          parsed ? parsed.year : now.getFullYear(),
          parsed ? parsed.month : now.getMonth());
      });
    }

    // Bind every `[data-gdp]` input inside a container (or document).
    function initAll(container) {
      (container || document).querySelectorAll('input[data-gdp]').forEach(attach);
    }

    // Close on outside click / Escape — bound once at module load.
    document.addEventListener('click', e => {
      if (activePopup && !activePopup.contains(e.target)) closePopup();
    });
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape' && activePopup) closePopup();
    });

    GG.datepicker = { attach, initAll };
  })();

  // --------------------------------------------------------- row_menu
  // Kebab-trigger action menu for table rows. Renders a single "⋮"
  // button into the caller's host element and a popup dropdown
  // containing the supplied items. Clicking the trigger toggles the
  // popup; outside click / Escape close it. Designed as a drop-in
  // replacement for inline `<button>` clusters — one narrow column
  // instead of 3-4 wrapping buttons.
  //
  // Usage:
  //   GG.row_menu.attach(host, [
  //     { label: 'Rename',     onClick: () => ... },
  //     { label: 'Promote',    onClick: () => ... },
  //     { label: 'Delete',     onClick: () => ..., danger: true },
  //   ]);
  //
  // `host` is any element (usually a `<td>`). Items with `hidden: true`
  // are skipped — lets callers compose the list conditionally without
  // branching their factory code.
  (function () {
    const esc = GG.core.escapeHtml;
    let openMenu = null;

    function closeOpen() {
      if (openMenu) {
        openMenu.classList.remove('open');
        openMenu = null;
      }
    }

    // Global listeners registered once: outside-click + Escape both
    // close the currently-open menu. Using capture phase on click so
    // the menu closes BEFORE anything else reacts (prevents a ghost
    // click from triggering another row's action).
    document.addEventListener('click', (e) => {
      if (openMenu && !openMenu.contains(e.target)) closeOpen();
    }, true);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closeOpen();
    });

    function attach(host, items) {
      if (!host) return;
      const visible = (items || []).filter(it => it && !it.hidden);
      if (visible.length === 0) return;

      const wrap = document.createElement('div');
      wrap.className = 'rowmenu';

      const trigger = document.createElement('button');
      trigger.type = 'button';
      trigger.className = 'rowmenu-trigger';
      trigger.setAttribute('aria-label', 'Row actions');
      trigger.innerHTML = '&#8942;'; // ⋮
      wrap.appendChild(trigger);

      const popup = document.createElement('div');
      popup.className = 'rowmenu-popup';
      for (const it of visible) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'rowmenu-item' + (it.danger ? ' danger' : '');
        b.textContent = it.label;
        b.addEventListener('click', (e) => {
          e.stopPropagation();
          closeOpen();
          if (typeof it.onClick === 'function') it.onClick();
        });
        popup.appendChild(b);
      }
      wrap.appendChild(popup);

      trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        const isOpen = wrap.classList.contains('open');
        closeOpen();
        if (!isOpen) {
          wrap.classList.add('open');
          openMenu = wrap;
        }
      });

      host.appendChild(wrap);
    }

    GG.row_menu = { attach };
  })();

  // -------------------------------------------------------------- theme
  // Persists light/dark to localStorage under "gigot.theme" and mirrors it
  // to <html data-theme=…>. No server-side config — the admin's own
  // preference, stored per-browser, same as goop2's viewer theme.
  (function () {
    const KEY = 'gigot.theme';
    const EVT = 'gigot:theme';

    function normalize(t) { return t === 'light' || t === 'dark' ? t : 'dark'; }

    function get() {
      const dom = document.documentElement.getAttribute('data-theme');
      if (dom === 'light' || dom === 'dark') return dom;
      return normalize(GG.core.safeLocalStorageGet(KEY));
    }
    function set(t) {
      t = normalize(t);
      document.documentElement.setAttribute('data-theme', t);
      GG.core.safeLocalStorageSet(KEY, t);
      window.dispatchEvent(new CustomEvent(EVT, { detail: { theme: t } }));
    }
    // Call from a <script> block in <head> before body renders so we
    // don't flash the wrong theme on first paint. No-op if localStorage
    // is unavailable — CSS defaults to dark.
    function early() {
      try {
        const stored = localStorage.getItem(KEY);
        if (stored === 'light' || stored === 'dark') {
          document.documentElement.setAttribute('data-theme', stored);
        }
      } catch { /* ignore */ }
    }
    function initToggle(idOrEl) {
      const cb = typeof idOrEl === 'string'
        ? document.querySelector('#' + idOrEl + ' input[type="checkbox"], #' + idOrEl)
        : idOrEl;
      if (!cb || cb.type !== 'checkbox') return;
      cb.checked = (get() === 'light');
      cb.addEventListener('change', () => set(cb.checked ? 'light' : 'dark'));
      window.addEventListener(EVT, (e) => {
        const t = e && e.detail && e.detail.theme === 'light' ? 'light' : 'dark';
        cb.checked = (t === 'light');
      });
    }
    GG.theme = { get, set, early, initToggle, EVT };
  })();
})();
