// /user — self-serve account page. Role-aware sidebar via
// Admin.initSidebar; the body only shows things a regular user is
// entitled to see (their own subscription keys). Admins landing
// here get the same view plus the full nav in the sidebar.
(function () {
  const { renderTokenCard, initSidebar } = window.Admin;

  async function loadMe() {
    const res = await fetch('/api/me', { credentials: 'include' });
    if (res.status === 401) {
      location.href = '/admin';
      return null;
    }
    if (!res.ok) throw new Error('me fetch failed: ' + res.status);
    return res.json();
  }

  function renderSubscriptions(subs) {
    const grid = document.getElementById('sub-grid');
    const empty = document.getElementById('sub-empty');
    grid.replaceChildren();
    if (!subs || subs.length === 0) {
      empty.classList.remove('hidden');
      return;
    }
    empty.classList.add('hidden');
    for (const t of subs) {
      // No subtitle, no extra chips, no actions — a regular user
      // owns these keys and can only copy them. An admin who wants
      // to rescope or revoke uses /admin/subscriptions.
      grid.appendChild(renderTokenCard(t, { title: 'Subscription key' }));
    }
  }

  (async function boot() {
    const me = await loadMe();
    if (!me) return;
    initSidebar('me', me);
    renderSubscriptions(me.subscriptions);
  })();
})();
