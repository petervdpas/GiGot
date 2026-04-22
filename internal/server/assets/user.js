// /user — the self-serve landing page for any signed-in account.
// Shows the caller's profile strip and subscription keys. Admins see
// the same page but get a link to the admin console too.
(function () {
  const { api, escapeHtml, copyToClipboard } = window.Admin;

  async function loadMe() {
    const res = await fetch('/api/me', { credentials: 'include' });
    if (res.status === 401) {
      location.href = '/admin';
      return null;
    }
    if (!res.ok) {
      throw new Error('me fetch failed: ' + res.status);
    }
    return res.json();
  }

  function renderProfile(me) {
    const label = me.display_name || me.username;
    const providerBadge = '<code>' + escapeHtml(me.provider || '') + '</code>';
    const roleBadge = '<span class="badge ' + (me.role === 'admin' ? 'formidable' : '') + '">' +
      escapeHtml(me.role) + '</span>';
    document.getElementById('me-strip').innerHTML =
      'Signed in as <strong>' + escapeHtml(label) + '</strong> · ' +
      providerBadge + ' · ' + roleBadge;
    if (me.role === 'admin') {
      document.getElementById('admin-link').classList.remove('hidden');
    }
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
      grid.appendChild(renderSubCard(t));
    }
  }

  function renderSubCard(t) {
    const card = document.createElement('div');
    card.className = 'sub-card';

    const repos = (t.repos && t.repos.length)
      ? t.repos.map(r => '<code>' + escapeHtml(r) + '</code>').join(' ')
      : '<span class="muted">all repositories</span>';
    const abilities = (t.abilities && t.abilities.length)
      ? t.abilities.map(a => '<span class="badge">' + escapeHtml(a) + '</span>').join(' ')
      : '<span class="muted">none</span>';

    card.innerHTML =
      '<div class="sub-card-header">' +
        '<div class="sub-label">Subscription key</div>' +
        '<button class="small secondary sub-copy">Copy</button>' +
      '</div>' +
      '<div class="sub-token"><code>' + escapeHtml(t.token) + '</code></div>' +
      '<div class="sub-meta">' +
        '<div><span class="muted">Repos:</span> ' + repos + '</div>' +
        '<div><span class="muted">Abilities:</span> ' + abilities + '</div>' +
      '</div>';

    card.querySelector('.sub-copy').addEventListener('click', async () => {
      const ok = await copyToClipboard(t.token);
      GG.dialog.alert(ok ? 'Copied' : 'Copy failed',
        ok ? 'The subscription key is now on your clipboard.' : 'Your browser blocked clipboard access.');
    });

    return card;
  }

  (async function boot() {
    const me = await loadMe();
    if (!me) return;
    renderProfile(me);
    renderSubscriptions(me.subscriptions);

    document.getElementById('logout').addEventListener('click', async () => {
      await api.logout();
      location.href = '/admin';
    });
  })();
})();
