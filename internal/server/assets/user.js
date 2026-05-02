// /user — self-serve account page. Role-aware sidebar via
// Admin.bootPage; the body only shows things a regular user is
// entitled to see (their own subscription keys). Admins landing
// here get the same view plus the full nav in the sidebar.
//
// Layout differs from /admin/subscriptions: this page is "copy
// these three values into Formidable" in order — Server URL (top
// card, once for the whole page), then per-key Repository +
// Subscription key copy rows. Each key card is collapsible:
// summary shows the repo name + abilities; body shows the copy
// rows. Collapsed-by-default so a user with many repos scans by
// repo name first, then expands the one they need.
//
// /user does NOT reuse Admin.renderTokenCard — that helper is
// admin-specific (tags + abilities collapse + Bind/Revoke actions),
// and twisting it into the regular-user shape via post-render DOM
// mutation produced nested <details>. This page has its own
// `user-subscription-card` fragment + renderer. Same GG.lazy +
// drawer style, just /user-shaped data.
(function () {
  const Admin = window.Admin;
  const { escapeHtml, copyToClipboard } = Admin;

  // Per-card expand state across this session. Keyed by token so
  // two cards with the same repo (different holders) don't share
  // state. Page boots with all cards closed; user expands what
  // they need.
  const cardOpenState = Object.create(null);

  async function loadMe() {
    const res = await fetch('/api/me', { credentials: 'include' });
    if (res.status === 401) {
      location.href = '/admin';
      return null;
    }
    if (!res.ok) throw new Error('me fetch failed: ' + res.status);
    return res.json();
  }

  // makeCopyRow builds the labelled copy row used for the page-level
  // Server URL block. Per-key Repository copy rows live inside the
  // user-subscription-card fragment instead of being assembled here.
  function makeCopyRow(label, value) {
    const wrap = document.createElement('div');
    wrap.className = 'copy-row';
    wrap.innerHTML =
      '<div class="copy-row-label">' + escapeHtml(label) + '</div>' +
      '<div class="token-field copy-row-field">' +
        '<code class="token-value">' + escapeHtml(value) + '</code>' +
        '<button type="button" class="copy-btn">Copy</button>' +
      '</div>';
    wrap.querySelector('.copy-btn').addEventListener('click', e =>
      copyToClipboard(value, e.currentTarget));
    return wrap;
  }

  // renderUserCard builds one subscription card. Outer <details>
  // collapses to summary (repo name + identifier subtitle +
  // abilities chips). Body lazy-renders from the
  // user-subscription-card fragment on first open; copy + eye
  // handlers wire in onRendered.
  function renderUserCard(t) {
    const card = document.createElement('details');
    card.className = 'info-card user-sub-card';
    card.dataset.token = t.token;
    card.dataset.lazyTpl = 'user-subscription-card';

    const repo = t.repo || '(unbound)';
    const acctSubtitle = t.username
      ? '<span class="muted sub-card-acct">' + escapeHtml(t.username) + '</span>'
      : '';
    const abilitiesChips = (t.abilities || [])
      .map(a => '<span class="ability-badge">' + escapeHtml(a) + '</span>')
      .join('');

    // Mask length scales with token length so the field width stays
    // plausible — same convention as the admin token card.
    const masked = '•'.repeat(Math.min(48, Math.max(16, (t.token || '').length)));

    card.innerHTML =
      '<summary class="ic-header user-sub-card-head">' +
        '<span class="card-chevron" aria-hidden="true">▶</span>' +
        '<div class="ic-title-wrap">' +
          '<div class="ic-title">' + escapeHtml(repo) + '</div>' +
          acctSubtitle +
        '</div>' +
        (abilitiesChips ? '<div class="ic-chips">' + abilitiesChips + '</div>' : '') +
      '</summary>';

    GG.lazy.bind(card, {
      getData: () => ({ repo, masked }),
      onRendered: host => wireUserCardBody(host, t, masked),
    });

    if (cardOpenState[t.token]) {
      card.open = true;
      GG.lazy.refresh(card);
    }
    card.addEventListener('toggle', () => { cardOpenState[t.token] = card.open; });

    return card;
  }

  // wireUserCardBody attaches the copy + eye-toggle handlers after
  // each body render. The Repository row's copy button copies the
  // repo name; the token field's eye toggles masked/revealed and
  // its copy button copies the actual token value (ignoring the
  // mask state — we want to copy the real secret either way).
  function wireUserCardBody(host, t, masked) {
    const repoCopy = host.querySelector('.copy-repo');
    if (repoCopy) {
      repoCopy.addEventListener('click', e => copyToClipboard(t.repo || '', e.currentTarget));
    }

    const field = host.querySelector('.token-field[data-revealed]');
    if (field) {
      const valueEl = field.querySelector('.token-value');
      const eyeBtn = field.querySelector('.eye-btn');
      const copyBtn = field.querySelector('.copy-token');
      if (valueEl && eyeBtn) {
        eyeBtn.addEventListener('click', () => {
          const revealed = field.dataset.revealed === '1';
          const next = !revealed;
          field.dataset.revealed = next ? '1' : '0';
          valueEl.textContent = next ? t.token : masked;
          valueEl.classList.toggle('token-masked', !next);
          eyeBtn.setAttribute('aria-label', next ? 'Hide key' : 'Show key');
          eyeBtn.setAttribute('title', next ? 'Hide key' : 'Show key');
          // Swap the SVG to the crossed-eye variant when revealed.
          // Not adding a separate icon helper — this is the only
          // /user surface that needs it, and inlining keeps the
          // SVG bytes in one place.
          eyeBtn.innerHTML = next
            ? '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
                '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>' +
                '<circle cx="12" cy="12" r="3"/>' +
                '<line x1="3" y1="3" x2="21" y2="21"/>' +
              '</svg>'
            : '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
                '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>' +
                '<circle cx="12" cy="12" r="3"/>' +
              '</svg>';
        });
      }
      if (copyBtn) {
        copyBtn.addEventListener('click', e => copyToClipboard(t.token, e.currentTarget));
      }
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
      grid.appendChild(renderUserCard(t));
    }
  }

  (async function boot() {
    const me = await loadMe();
    if (!me) return;
    Admin.initSidebar('me', me);

    // Server URL is the same host for every key this account holds
    // (they all talk to THIS gigot instance), so render it once at
    // the page level instead of duplicating per card.
    const serverHost = document.getElementById('server-url-host');
    if (serverHost) serverHost.replaceChildren(makeCopyRow('Server URL', window.location.origin));

    renderSubscriptions(me.subscriptions);
  })();
})();
