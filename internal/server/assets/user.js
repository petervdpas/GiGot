// /user — self-serve account page. Role-aware sidebar via
// Admin.initSidebar; the body only shows things a regular user is
// entitled to see (their own subscription keys). Admins landing
// here get the same view plus the full nav in the sidebar.
//
// The card layout differs from /admin/subscriptions: this page is
// "copy these three values into Formidable" in order — Server URL,
// Repository, Subscription key — so we render them as three
// labelled copy-rows instead of only exposing the token. Admins
// editing access go to /admin/subscriptions; regulars just need
// the paste-ready values.
//
// Each key card is collapsible: summary shows the repo name and
// abilities badge; expanded body shows the Repository + Subscription
// key copy rows. Collapsed-by-default so a user with many repos
// scans by repo name first, then expands only the one they need.
(function () {
  const Admin = window.Admin;
  const { renderTokenCard, initSidebar, escapeHtml, copyToClipboard } = Admin;

  // Per-repo expand state is remembered across session but not
  // across a reload — page boots with all cards closed, and the
  // user expands the one they need. Keyed by token string so two
  // cards with the same repo (different holders) don't share state.
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

  // makeCopyRow builds a labelled, copy-enabled row for a plain
  // (non-secret) value — no eye toggle, no masking. Uses the same
  // .token-field chrome as the secret row below so the stack reads
  // as three aligned "paste me into Formidable" inputs.
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

  // augmentCardForFormidable rewrites a standard token card into the
  // paste-ready layout: drops the repo chip row (redundant — the
  // repo is now a copy-row above the key), keeps the abilities
  // badge, and prepends a Repository + Subscription key label stack.
  // The server URL is rendered ONCE at the page level (it's the same
  // host for every key), not per card.
  function augmentCardForFormidable(card, t) {
    const tokenField = card.querySelector('.token-field');
    if (!tokenField) return;

    // Drop the redundant body chip rows — a regular user seeing
    // their own keys doesn't benefit from a "repo chip" that
    // repeats the repo they're about to copy from the row above it.
    const bodyChips = card.querySelectorAll(':scope > .ic-chips');
    bodyChips.forEach(el => el.remove());

    // Abilities go into the card HEADER (as a chip next to the
    // title) so they stay visible when the card is collapsed —
    // users scanning a list need to see at a glance which key has
    // mirror privileges.
    if (t.abilities && t.abilities.length) {
      const header = card.querySelector('.ic-header');
      if (header) {
        let chipsHost = header.querySelector('.ic-chips');
        if (!chipsHost) {
          chipsHost = document.createElement('div');
          chipsHost.className = 'ic-chips';
          header.appendChild(chipsHost);
        }
        chipsHost.innerHTML = t.abilities
          .map(a => '<span class="ability-badge">' + escapeHtml(a) + '</span>')
          .join('');
      }
    }

    tokenField.parentNode.insertBefore(makeCopyRow('Repository', t.repo || ''), tokenField);

    const tokenLabel = document.createElement('div');
    tokenLabel.className = 'copy-row-label';
    tokenLabel.textContent = 'Subscription key';
    tokenField.parentNode.insertBefore(tokenLabel, tokenField);
  }

  // wrapCardAsCollapsible takes a fully-rendered subscription card
  // and rewraps it as a <details>/<summary> disclosure. The card's
  // first child (the ic-header row with title + chips) becomes the
  // summary; the remaining rows become the body. Preserves the
  // inner DOM (including the token-field's eye toggle + copy
  // handlers), so behaviour survives the reshape.
  function wrapCardAsCollapsible(card, t) {
    const children = Array.from(card.children);
    if (children.length === 0) return;
    const [header, ...rest] = children;

    const details = document.createElement('details');
    details.className = 'ic-collapse sub-card-collapse';
    if (cardOpenState[t.token]) details.setAttribute('open', '');

    const summary = document.createElement('summary');
    summary.className = 'sub-card-summary';
    while (header.firstChild) summary.appendChild(header.firstChild);
    header.remove();

    const body = document.createElement('div');
    body.className = 'ic-collapse-body';
    for (const el of rest) body.appendChild(el);

    details.appendChild(summary);
    details.appendChild(body);
    card.appendChild(details);

    details.addEventListener('toggle', () => {
      cardOpenState[t.token] = details.open;
    });
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
      const repo = t.repo || 'Subscription key';
      // A user can hold accounts on multiple providers (github + microsoft,
      // etc.); show provider:identifier in small print after the repo name
      // so they can tell which account a key was issued to without
      // expanding the card. Falls back to plain repo when the token has
      // no scoped username (legacy / unbound shouldn't happen here, but
      // the empty state stays clean if it does).
      const titleHTML = t.username
        ? escapeHtml(repo) + ' <span class="muted sub-card-acct">' + escapeHtml(t.username) + '</span>'
        : escapeHtml(repo);
      const card = renderTokenCard(t, { title: repo, titleHTML });
      augmentCardForFormidable(card, t);
      wrapCardAsCollapsible(card, t);
      grid.appendChild(card);
    }
  }

  (async function boot() {
    const me = await loadMe();
    if (!me) return;
    initSidebar('me', me);

    // Server URL is the same host for every key this account holds
    // (they all talk to THIS gigot instance), so render it once at
    // the page level instead of duplicating per card.
    const serverHost = document.getElementById('server-url-host');
    if (serverHost) serverHost.replaceChildren(makeCopyRow('Server URL', window.location.origin));

    renderSubscriptions(me.subscriptions);
  })();
})();
