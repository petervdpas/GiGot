// GG.drawer — right-side slide-out panel for create / edit flows.
// Pairs with GG.lazy: the drawer's body is typically a lazy host
// (`data-lazy-tpl="..."`) so the form HTML lives in a fragment
// rather than the page template. Symmetric in spirit to
// GG.tag_picker / GG.tag_filter / GG.lazy — HTML carries the
// intent via `data-drawer-*`, this module reads it.
//
// Markup contract:
//
//   <button class="btn-add" data-drawer-open="create-account">+</button>
//
//   <aside class="drawer" data-drawer-name="create-account">
//     <div class="drawer-head">
//       <h2>Create account</h2>
//       <button type="button" class="drawer-close" aria-label="Close">×</button>
//     </div>
//     <div class="drawer-body" data-lazy-tpl="create-account"
//          data-lazy-trigger="manual"></div>
//   </aside>
//
//   GG.drawer.attachAll();
//
// Behaviour:
//   - Click the open button → drawer slides in, backdrop fades up,
//     the lazy body (if any) refreshes so the fragment renders.
//   - Click backdrop / press Esc / click `.drawer-close` → drawer
//     slides out, backdrop fades, page is interactive again.
//   - GG.drawer.close() programmatically closes whatever's open
//     (e.g. after a successful form submit).

(function () {
  // backdropEl is created lazily on first open so pages without a
  // drawer don't pay the DOM cost. One backdrop covers all
  // drawers — only one drawer is open at a time anyway.
  let backdropEl = null;

  function ensureBackdrop() {
    if (backdropEl) return backdropEl;
    backdropEl = document.createElement('div');
    backdropEl.className = 'drawer-backdrop';
    backdropEl.addEventListener('click', closeAll);
    document.body.appendChild(backdropEl);
    return backdropEl;
  }

  function escHandler(e) {
    if (e.key === 'Escape') closeAll();
  }

  // open finds the drawer by name, slides it in, refreshes its
  // lazy body if present, and focuses the first input so the
  // admin can start typing immediately.
  function open(name) {
    const drawer = document.querySelector('.drawer[data-drawer-name="' + cssEscape(name) + '"]');
    if (!drawer) return;
    closeAll(); // ensure no other drawer is open
    ensureBackdrop().classList.add('open');
    drawer.classList.add('open');
    document.addEventListener('keydown', escHandler);
    // Refresh any lazy host inside the drawer so the fragment
    // re-renders against current page state every time the drawer
    // opens (form fields reset, etc.).
    const lazyHost = drawer.querySelector('[data-lazy-tpl]');
    if (lazyHost && window.GG && GG.lazy) GG.lazy.refresh(lazyHost);
    // Focus management: prefer the first focusable input inside
    // the drawer body. Wait for the lazy render to populate the
    // body — a microtask delay is enough because GG.lazy.refresh
    // resolves synchronously after fetchFragment + render() (the
    // fragment cache hits on the second open onward; first open
    // costs one fetch, then focus lands).
    setTimeout(() => {
      const focusable = drawer.querySelector(
        'input:not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled])',
      );
      if (focusable) focusable.focus();
    }, 60);
  }

  // closeAll closes every drawer + the backdrop. Exposed as
  // GG.drawer.close() for explicit "close after submit" flows.
  function closeAll() {
    document.querySelectorAll('.drawer.open').forEach(d => d.classList.remove('open'));
    if (backdropEl) backdropEl.classList.remove('open');
    document.removeEventListener('keydown', escHandler);
  }

  // cssEscape protects the name in the attribute selector. The
  // drawer name is admin-controlled (it's coded into the page
  // template) so this is belt-and-braces, but the helper is
  // generic and could one day take user input.
  function cssEscape(s) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(s);
    return String(s).replace(/"/g, '\\"');
  }

  // attachAll walks the document for `[data-drawer-open]` open
  // buttons and `.drawer-close` close buttons and wires their
  // click handlers. Idempotent — re-running on a page with
  // already-attached drawers is a no-op.
  function attachAll(root) {
    root = root || document;
    root.querySelectorAll('[data-drawer-open]').forEach(btn => {
      if (btn.__drawerWired) return;
      btn.__drawerWired = true;
      btn.addEventListener('click', () => open(btn.dataset.drawerOpen));
    });
    root.querySelectorAll('.drawer-close').forEach(btn => {
      if (btn.__drawerWired) return;
      btn.__drawerWired = true;
      btn.addEventListener('click', closeAll);
    });
  }

  window.GG = window.GG || {};
  window.GG.drawer = { attachAll, open, close: closeAll };
})();
