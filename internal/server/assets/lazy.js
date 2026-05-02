// GG.lazy — generic data-attribute-driven render helper. Lifts the
// "fetch + render + bind events on collapse open" pattern out of
// per-page imperative DOM glue. Symmetric to GG.tag_picker /
// GG.tag_filter; HTML carries the intent via `data-*`, this one
// tiny module reads it.
//
// See docs/design/lazy.md for the full contract.
//
// Hello world:
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

    const trigger = host.dataset.lazyTrigger || defaultTrigger(host);

    async function run() {
      try {
        const data = src
          ? await (await fetch(substituteURL(src, host), { credentials: 'same-origin' })).json()
          : await opts.getData(host);
        const tpl = await fetchFragment(tplName);
        const html = render(tpl, data || {});
        // Replace any prior render. For <details> we render into a
        // dedicated child div so <summary> isn't clobbered.
        const target = host.tagName === 'DETAILS'
          ? ensureDetailsBody(host)
          : host;
        target.innerHTML = html;
        if (opts.onRendered) opts.onRendered(host, data);
      } catch (e) {
        // Surface render failures so a misconfig isn't a silent
        // empty card. The error lands in the console and a small
        // hint replaces the loading state so the user sees something.
        console.error('lazy render failed', e);
        const target = host.tagName === 'DETAILS' ? ensureDetailsBody(host) : host;
        target.innerHTML = '<div class="muted">Render failed — see console.</div>';
      }
    }

    bindTrigger(host, trigger, run);
    // Stash a refresh hook on the element so refresh(host) can find
    // it without rewalking the bind config.
    host.__lazyRun = run;
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

  window.GG = window.GG || {};
  window.GG.lazy = {
    bind, attachAll, refresh,
    cache: { clear: () => fragments.clear() },
    // Exposed for tests / future extension; not part of the
    // public contract.
    _internals: { render, substituteURL },
  };
})();
