// GG.lazy — generic data-attribute-driven render helper. Lifts the
// "fetch + render + bind events on collapse open" pattern out of
// per-page imperative DOM glue. Symmetric to GG.tag_picker /
// GG.tag_filter; HTML carries the intent via `data-*`, this one
// tiny module reads it.
//
// See docs/design/lazy.md for the full contract.
//
// Hello world (read flow):
//
//   <details data-lazy-tpl="abilities" data-token="abc123">
//     <summary>Abilities</summary>
//   </details>
//
//   GG.lazy.bind(detailsEl, {
//     getData: host => ({ abilities: [{name: "mirror", checked: "checked"}] }),
//     onRendered: host => { /* attach behaviour to the rendered DOM */ },
//   });
//
// Or, when the data is safe to ride a URL (account / repo identifier):
//
//   <details data-lazy-tpl="account-detail"
//            data-lazy-src="/api/admin/accounts/{provider}/{identifier}"
//            data-provider="local" data-identifier="alice">
//     <summary>Details</summary>
//   </details>
//
//   GG.lazy.attachAll();
//
// Hello world (write flow — slice 2):
//
//   <details data-lazy-tpl="abilities"
//            data-lazy-submit="/api/admin/tokens"
//            data-lazy-submit-method="PATCH"
//            data-lazy-after="event:abilities-saved"
//            data-token="abc123">
//     <summary>Abilities</summary>
//   </details>
//
//   <!-- inside the abilities fragment: -->
//   <button type="button" data-lazy-action="submit">Save</button>
//   <span data-lazy-msg></span>
//
// On click of `[data-lazy-action="submit"]` (or a `<form>` submit):
// the helper collects every `[name]` input inside the rendered body,
// merges in the host's non-`lazy-*` `data-*` attributes, fetches the
// `data-lazy-submit` endpoint with `data-lazy-submit-method` (default
// POST), then runs `data-lazy-after` (default `render`):
//   - `render`            — re-render the fragment against the response
//   - `refresh`           — re-run the read path (fetch + render fresh data)
//   - `close`             — collapse the host (or close enclosing drawer)
//   - `event:<name>`      — dispatch a CustomEvent with `{request, response}`
// Errors land in any `[data-lazy-msg]` element inside the rendered body
// AND fire a bubbling `lazy-submit-error` CustomEvent on the host.

(function () {
  const esc = (window.GG && window.GG.core && window.GG.core.escapeHtml) ||
    (s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c])));

  // ────────────────────────────────────────────────────────── fragments
  // Fragment cache is name → string. First fetch populates it; later
  // requests rely on the browser's HTTP cache (the server returns 304
  // after the first download). Module-private; tests can clear via
  // GG.lazy.cache.clear().
  const fragments = new Map();

  async function fetchFragment(name) {
    if (fragments.has(name)) return fragments.get(name);
    const r = await fetch('/fragments/' + encodeURIComponent(name), {
      credentials: 'same-origin',
    });
    if (!r.ok) throw new Error('fragment ' + name + ': ' + r.status);
    const body = await r.text();
    fragments.set(name, body);
    return body;
  }

  // ────────────────────────────────────────────────────── templating
  //
  // Two operations: `{{key}}` (HTML-escaped substitution, dot paths)
  // and `{{#each items}}…{{/each}}` (array iteration). No `{{#if}}`,
  // no partials, no unescaped output — see design doc §5 for the
  // scope rationale. Implementation is deliberately under 50 lines.

  // resolveDot reads a dot-path off ctx. `{{this}}` returns the
  // context itself, which is what an {{#each items}} over a
  // primitive array needs.
  function resolveDot(ctx, key) {
    if (key === 'this') return ctx;
    let v = ctx;
    for (const part of key.split('.')) {
      if (v == null) return '';
      v = v[part];
    }
    return v == null ? '' : v;
  }

  // render walks tpl and emits HTML. ctx is the data object for the
  // current scope (top-level on first call, an item inside #each).
  // Each "section" is matched non-greedily to allow nested fragments
  // in the same template; nesting itself is not supported (one level
  // of {{#each}} is enough for v1).
  function render(tpl, ctx) {
    // Step 1: handle {{#each items}}…{{/each}} sections by replacing
    // each one with the concatenation of its body rendered against
    // each item.
    const eachRE = /\{\{#each\s+([\w.]+)\s*\}\}([\s\S]*?)\{\{\/each\}\}/g;
    let out = tpl.replace(eachRE, (_, key, body) => {
      const arr = resolveDot(ctx, key);
      if (!Array.isArray(arr)) return '';
      return arr.map(item => render(body, item)).join('');
    });
    // Step 2: handle plain {{key}} substitutions in what remains.
    return out.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_, key) => {
      return esc(resolveDot(ctx, key));
    });
  }

  // ──────────────────────────────────────────────────── url substitution

  // substituteURL resolves {key} placeholders in a URL against the
  // host's data-* attributes (kebab-cased lookup, so {tokenId} reads
  // data-token-id). Missing keys throw at bind time so misconfigs
  // fail loud rather than silently producing 404s on click.
  function substituteURL(url, host) {
    return url.replace(/\{([\w-]+)\}/g, (_, key) => {
      const datasetKey = key.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      const v = host.dataset[datasetKey];
      if (v == null) {
        throw new Error('lazy: missing data-' + key + ' for URL placeholder');
      }
      return encodeURIComponent(v);
    });
  }

  // ───────────────────────────────────────────────────────── triggers
  //
  // Default: <details> binds to its `toggle` event (firing only on
  // open, not close). Anything else binds to `click`. `now` runs
  // immediately at bind time (used for elements that should hydrate
  // on page load).

  function defaultTrigger(host) {
    return host.tagName === 'DETAILS' ? 'open' : 'click';
  }

  function bindTrigger(host, kind, run) {
    if (kind === 'now') {
      run();
      return;
    }
    if (kind === 'manual') {
      // No auto-binding. Caller drives renders via
      // GG.lazy.refresh(host). Used when the visible "open" gesture
      // lives on a sibling element (e.g. a toggle button outside
      // the host) and the helper would otherwise wire the wrong
      // listener.
      return;
    }
    if (kind === 'open' && host.tagName === 'DETAILS') {
      // <details>.toggle fires on both open and close; we only care
      // about open. addEventListener("toggle") fires per state
      // change; reading host.open inside the handler tells us which.
      let primed = false;
      host.addEventListener('toggle', () => {
        if (host.open && !primed) {
          primed = true;
          run();
        }
      });
      return;
    }
    // Generic click trigger — single-fire by default. Re-render on
    // click is achieved via an explicit GG.lazy.refresh(host) call.
    let primed = false;
    host.addEventListener('click', e => {
      if (primed) return;
      primed = true;
      run();
    });
  }

  // ───────────────────────────────────────────────────────── bind / attach

  // bind wires one host element. Must specify exactly one of
  // `getData` (programmatic) or `data-lazy-src` (declarative URL).
  function bind(host, opts) {
    opts = opts || {};
    const tplName = host.dataset.lazyTpl;
    if (!tplName) throw new Error('lazy: data-lazy-tpl missing');
    const src = host.dataset.lazySrc;
    if (src && opts.getData) {
      throw new Error('lazy: cannot mix data-lazy-src with getData');
    }
    if (!src && !opts.getData) {
      throw new Error('lazy: bind requires data-lazy-src or opts.getData');
    }
    // Pre-validate URL placeholders at bind time, not click time.
    if (src) substituteURL(src, host);
    if (host.dataset.lazySubmit) substituteURL(host.dataset.lazySubmit, host);

    const trigger = host.dataset.lazyTrigger || defaultTrigger(host);

    // renderInto paints `data` into the host using the cached
    // fragment. Shared between the read path (run) and the
    // post-submit `render` after-action. After painting, wire any
    // submit triggers ([data-lazy-action="submit"] + nested forms)
    // so they're live for the freshly rendered DOM.
    async function renderInto(data) {
      const tpl = await fetchFragment(tplName);
      const html = render(tpl, data || {});
      const target = host.tagName === 'DETAILS'
        ? ensureDetailsBody(host)
        : host;
      target.innerHTML = html;
      if (host.dataset.lazySubmit) wireSubmitTriggers(host, target);
      if (opts.onRendered) opts.onRendered(host, data);
    }

    async function run() {
      try {
        const data = src
          ? await (await fetch(substituteURL(src, host), { credentials: 'same-origin' })).json()
          : await opts.getData(host);
        await renderInto(data);
      } catch (e) {
        // Surface render failures so a misconfig isn't a silent
        // empty card. The error lands in the console and a small
        // hint replaces the loading state so the user sees something.
        console.error('lazy render failed', e);
        const target = host.tagName === 'DETAILS' ? ensureDetailsBody(host) : host;
        target.innerHTML = '<div class="muted">Render failed. See console.</div>';
      }
    }

    bindTrigger(host, trigger, run);
    // Stash hooks on the element so refresh(host) and the submit
    // path can find them without rewalking the bind config.
    host.__lazyRun = run;
    host.__lazyRender = renderInto;
  }

  // ensureDetailsBody returns the `.lazy-body` div inside a
  // <details> host, creating it on first use. Renders go into this
  // div so the <summary> element survives subsequent re-renders.
  function ensureDetailsBody(host) {
    let body = host.querySelector(':scope > .lazy-body');
    if (body) return body;
    body = document.createElement('div');
    body.className = 'lazy-body';
    host.appendChild(body);
    return body;
  }

  // attachAll walks the document (or a given root) and auto-binds
  // every [data-lazy-tpl] element that doesn't have a programmatic
  // bind already attached. Used on page boot. Elements that need a
  // getData callback should be bound via bind() BEFORE attachAll
  // runs, or attachAll will skip them (no `__lazyRun` stamp = not
  // bound, but no `data-lazy-src` either = config error, which
  // attachAll silently leaves alone — the explicit bind() will
  // throw if something's wrong).
  function attachAll(root) {
    root = root || document;
    root.querySelectorAll('[data-lazy-tpl]').forEach(host => {
      if (host.__lazyRun) return;          // already bound programmatically
      if (!host.dataset.lazySrc) return;   // needs explicit bind() with getData
      bind(host, {});
    });
  }

  function refresh(host) {
    if (host && host.__lazyRun) host.__lazyRun();
  }

  // ───────────────────────────────────────────────────────── submit
  //
  // Slice 2: write path. The helper collects every `[name]` input
  // inside the rendered body, merges in the host's non-`lazy-*`
  // `data-*` attributes, fetches the configured endpoint, then runs
  // `data-lazy-after` against the response.
  //
  // Submit triggers are wired AFTER each render so the wiring
  // attaches to the freshly painted DOM (re-renders replace the
  // listener targets).

  // collectSubmitBody walks every [name] input inside `target` and
  // packages the result as JSON, then merges in the host's payload-
  // worthy data-* attributes. Three rules drive the input encoding:
  //
  //   - Checkbox of any name           → array of checked values.
  //                                     Always an array, even if there
  //                                     is exactly one checkbox of
  //                                     that name; the abilities
  //                                     picker uses this.
  //   - Radio of a given name          → string value of the checked
  //                                     option (or omitted if none).
  //   - Anything else                  → string value of the field.
  //
  // The host-data merge appends every `data-*` whose key doesn't
  // start with `lazy` (those are the helper's own metadata) and
  // whose name isn't already in the form payload (form fields win).
  // Only the host's own dataset is consulted — child elements'
  // data-* are not aggregated; if a value needs to ride to the
  // server, put it on the host or render it as a [name] input.
  function collectSubmitBody(host, target) {
    const data = {};
    target.querySelectorAll('[name]').forEach(el => {
      const name = el.name;
      if (el.type === 'checkbox') {
        if (!Array.isArray(data[name])) data[name] = [];
        if (el.checked) data[name].push(el.value);
      } else if (el.type === 'radio') {
        if (el.checked) data[name] = el.value;
      } else {
        data[name] = el.value;
      }
    });
    for (const key in host.dataset) {
      if (key.startsWith('lazy')) continue;
      if (data[key] === undefined) data[key] = host.dataset[key];
    }
    return data;
  }

  // setSubmitMessage writes `text` into every [data-lazy-msg] element
  // inside `target` and toggles the error class on. Empty text clears
  // both. Multiple targets are unusual in practice but cheap to
  // support; the abilities footer uses one, drawer forms might add a
  // second above the action row.
  function setSubmitMessage(target, text, kind) {
    target.querySelectorAll('[data-lazy-msg]').forEach(el => {
      el.textContent = text || '';
      el.classList.toggle('error', kind === 'error');
      el.classList.toggle('muted', kind !== 'error');
    });
  }

  // runAfter applies the configured post-submit behaviour. The
  // `event:<name>` form dispatches a bubbling CustomEvent so a page-
  // level listener can react (typically: mutate in-memory state and
  // call GG.lazy.refresh). Unknown values fall through to a no-op so
  // a typo doesn't strand the request — the network call still
  // happened, the visible state just doesn't auto-update.
  async function runAfter(host, after, request, response) {
    after = after || 'render';
    if (after === 'render') {
      if (host.__lazyRender) await host.__lazyRender(response || {});
      return;
    }
    if (after === 'refresh') {
      if (host.__lazyRun) await host.__lazyRun();
      return;
    }
    if (after === 'close') {
      if (host.tagName === 'DETAILS') host.open = false;
      const drawer = host.closest && host.closest('.drawer');
      if (drawer && window.GG && GG.drawer && GG.drawer.close) GG.drawer.close();
      return;
    }
    if (after.indexOf('event:') === 0) {
      const name = after.slice('event:'.length);
      host.dispatchEvent(new CustomEvent(name, {
        bubbles: true,
        detail: { request, response },
      }));
    }
  }

  // submit fires the configured POST/PATCH/etc., handles errors, and
  // dispatches the after-action. Single in-flight guard via
  // `host.__lazySubmitting` so a second click during the network round
  // trip is dropped (matches the existing imperative ability-save
  // pattern that disabled the button).
  async function submit(host) {
    if (host.__lazySubmitting) return;
    const url = substituteURL(host.dataset.lazySubmit, host);
    const method = (host.dataset.lazySubmitMethod || 'POST').toUpperCase();
    const target = host.tagName === 'DETAILS' ? ensureDetailsBody(host) : host;
    const body = collectSubmitBody(host, target);
    setSubmitMessage(target, '', 'muted');
    host.__lazySubmitting = true;
    try {
      const r = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      let payload = null;
      try { payload = await r.json(); } catch { payload = null; }
      if (!r.ok) {
        const msg = (payload && payload.error) || ('submit failed: ' + r.status);
        throw new Error(msg);
      }
      await runAfter(host, host.dataset.lazyAfter, body, payload || {});
    } catch (err) {
      setSubmitMessage(target, err.message || String(err), 'error');
      host.dispatchEvent(new CustomEvent('lazy-submit-error', {
        bubbles: true,
        detail: { error: err, request: body },
      }));
    } finally {
      host.__lazySubmitting = false;
    }
  }

  // wireSubmitTriggers binds the freshly rendered DOM. Buttons with
  // `data-lazy-action="submit"` fire submit on click; nested `<form>`
  // elements fire on the form's `submit` event so Enter-in-input
  // also commits. Rebound on every render — listeners attach to the
  // current DOM nodes, not stale ones from a previous render.
  function wireSubmitTriggers(host, target) {
    target.querySelectorAll('[data-lazy-action="submit"]').forEach(btn => {
      btn.addEventListener('click', e => {
        e.preventDefault();
        submit(host);
      });
    });
    target.querySelectorAll('form').forEach(form => {
      form.addEventListener('submit', e => {
        e.preventDefault();
        submit(host);
      });
    });
  }

  window.GG = window.GG || {};
  window.GG.lazy = {
    bind, attachAll, refresh, submit,
    cache: { clear: () => fragments.clear() },
    // Exposed for tests / future extension; not part of the
    // public contract.
    _internals: { render, substituteURL, collectSubmitBody },
  };
})();
