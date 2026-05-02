// Shared API client + sidebar + session guard. Loaded before every
// page-specific admin script so repositories.js / subscriptions.js /
// credentials.js can focus on their own rendering logic without
// duplicating the fetch layer or the sidebar wiring.
//
// Exposes one global: window.Admin = { api, escapeHtml, shortSha,
// initSidebar, guardSession, copyToClipboard }.

(function () {
  // ---------------------------------------------------------------- api
  // Every endpoint is in one object. Pages call the ones they need —
  // splitting per-page was more churn than value because most endpoints
  // are cross-referenced (repositories.js needs listTokens +
  // listCredentials to paint subscription chips + destination dropdowns).
  const api = {
    async session() {
      const r = await fetch('/api/admin/session', { credentials: 'same-origin' });
      return r.ok ? r.json() : null;
    },
    async listOAuthProviders() {
      // Public endpoint — the login page calls this before any session
      // exists, and /api/admin/providers is MarkPublic'd for exactly
      // that reason. Returns { providers: [...] } — possibly empty.
      const r = await fetch('/api/admin/providers', { credentials: 'same-origin' });
      if (!r.ok) return { providers: [] };
      return r.json();
    },
    async login(username, password) {
      const r = await fetch('/admin/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ username, password }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'login failed');
      return r.json();
    },
    async logout() {
      await fetch('/admin/logout', { method: 'POST', credentials: 'same-origin' });
    },
    async listRepos() {
      const r = await fetch('/api/repos', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list repos failed');
      return r.json();
    },
    async createRepo(name, scaffoldFormidable, sourceURL) {
      const body = { name };
      // Only send scaffold_formidable when the admin explicitly ticked
      // the toggle. Omitting it lets the server apply its configured
      // default (server.formidable_first, see design doc §2.7).
      if (scaffoldFormidable) body.scaffold_formidable = true;
      if (sourceURL) body.source_url = sourceURL;
      const r = await fetch('/api/repos', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'create failed');
      return r.json();
    },
    async deleteRepo(name) {
      const r = await fetch('/api/repos/' + encodeURIComponent(name), {
        method: 'DELETE', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error('delete failed');
    },
    async listTokens() {
      const r = await fetch('/api/admin/tokens', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list failed');
      return r.json();
    },
    async issueToken(username, repo, abilities) {
      const r = await fetch('/api/admin/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ username, repo, abilities }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'issue failed');
      return r.json();
    },
    async updateToken(token, patch) {
      const body = Object.assign({ token }, patch);
      const r = await fetch('/api/admin/tokens', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'update failed');
    },
    async revokeToken(token) {
      const r = await fetch('/api/admin/tokens', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ token }),
      });
      if (!r.ok) throw new Error('revoke failed');
    },
    // listTokensByTags hits the existing /api/admin/tokens with one
    // ?tag= parameter per tag. Tags are AND-ed server-side; pass an
    // empty array to get the unfiltered list (same as listTokens).
    async listTokensByTags(tagsArr) {
      const qs = (tagsArr || [])
        .filter(Boolean)
        .map(t => 'tag=' + encodeURIComponent(t))
        .join('&');
      const url = '/api/admin/tokens' + (qs ? '?' + qs : '');
      const r = await fetch(url, { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list failed');
      return r.json();
    },
    async revokeTokensByTag(tagsArr, confirm) {
      const r = await fetch('/api/admin/subscriptions/revoke-by-tag', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ tags: tagsArr, confirm }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'revoke-by-tag failed');
      return r.json();
    },
    async listDestinations(repo) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/destinations', {
        credentials: 'same-origin',
      });
      if (!r.ok) throw new Error('list destinations failed');
      return r.json();
    },
    async createDestination(repo, body) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/destinations', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'create destination failed');
      return r.json();
    },
    async updateDestination(repo, id, body) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/destinations/' + encodeURIComponent(id), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'update destination failed');
      return r.json();
    },
    async deleteDestination(repo, id) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/destinations/' + encodeURIComponent(id), {
        method: 'DELETE', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error('delete destination failed');
    },
    async syncDestination(repo, id) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/destinations/' + encodeURIComponent(id) + '/sync', {
        method: 'POST', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error((await r.json()).error || 'sync failed');
      return r.json();
    },
    async convertToFormidable(repo) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/formidable', {
        method: 'POST', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error((await r.json()).error || 'convert failed');
      return r.json();
    },
    async listCredentials() {
      const r = await fetch('/api/admin/credentials', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list credentials failed');
      return r.json();
    },
    async createCredential(body) {
      const r = await fetch('/api/admin/credentials', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'create failed');
      return r.json();
    },
    async deleteCredential(name) {
      const r = await fetch('/api/admin/credentials/' + encodeURIComponent(name), {
        method: 'DELETE', credentials: 'same-origin',
      });
      if (r.ok) return;
      // 409 ships { error, ref_repos } — surface both so the caller
      // can tell the operator which destinations still reference the
      // credential. Other errors fall back to the raw error field.
      let body = {};
      try { body = await r.json(); } catch { /* non-JSON body */ }
      const err = new Error(body.error || ('delete failed (' + r.status + ')'));
      if (Array.isArray(body.ref_repos)) err.refRepos = body.ref_repos;
      throw err;
    },
    async listTags() {
      const r = await fetch('/api/admin/tags', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list tags failed');
      return r.json();
    },
    async createTag(name) {
      const r = await fetch('/api/admin/tags', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ name }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'create failed');
      return r.json();
    },
    async renameTag(id, name) {
      const r = await fetch('/api/admin/tags/' + encodeURIComponent(id), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ name }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'rename failed');
      return r.json();
    },
    async deleteTag(id) {
      const r = await fetch('/api/admin/tags/' + encodeURIComponent(id), {
        method: 'DELETE', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error((await r.json()).error || 'delete failed');
      return r.json();
    },
    async setRepoTags(repo, tags) {
      const r = await fetch('/api/admin/repos/' + encodeURIComponent(repo) + '/tags', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ tags }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'set repo tags failed');
      return r.json();
    },
    async setAccountTags(provider, identifier, tags) {
      const r = await fetch('/api/admin/accounts/' + encodeURIComponent(provider) + '/' + encodeURIComponent(identifier) + '/tags', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ tags }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'set account tags failed');
      return r.json();
    },
    async listAccounts() {
      const r = await fetch('/api/admin/accounts', { credentials: 'same-origin' });
      if (!r.ok) throw new Error('list accounts failed');
      return r.json();
    },
    async createAccount(body) {
      const r = await fetch('/api/admin/accounts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'create failed');
      return r.json();
    },
    async patchAccount(provider, identifier, body) {
      const r = await fetch('/api/admin/accounts/' + encodeURIComponent(provider) + '/' + encodeURIComponent(identifier), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'patch failed');
      return r.json();
    },
    async deleteAccount(provider, identifier) {
      const r = await fetch('/api/admin/accounts/' + encodeURIComponent(provider) + '/' + encodeURIComponent(identifier), {
        method: 'DELETE', credentials: 'same-origin',
      });
      if (!r.ok) throw new Error((await r.json()).error || 'delete failed');
    },
    async bindToken(token) {
      const r = await fetch('/api/admin/tokens/bind', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ token }),
      });
      if (!r.ok) throw new Error((await r.json()).error || 'bind failed');
      return r.json();
    },
  };

  // ---------------------------------------------------------- helpers
  function escapeHtml(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }
  function shortSha(sha) { return sha ? sha.slice(0, 7) : ''; }

  // brandVersion reads the build-stamped version off the
  // `<meta name="gigot-version">` tag that every admin template
  // emits. Returns "" when the meta is absent or its content is
  // empty (test fixtures, unset build) so callers can append
  // unconditionally without producing a stray "v" with no number.
  function brandVersion() {
    const m = document.querySelector('meta[name="gigot-version"]');
    return (m && m.content) ? m.content : '';
  }

  // brandVersionSuffix returns the HTML fragment to append after a
  // "GiGot" brand label — escaped, themed, and prefixed with a
  // leading space. Empty when no version is set so the brand strip
  // collapses cleanly. One source of truth for the JS-rendered
  // surfaces (sidebar, future modals); server-side templates use the
  // same shape via {{.Version}} on the Go side.
  function brandVersionSuffix() {
    const v = brandVersion();
    if (!v) return '';
    return ' <span class="brand-version muted">' + escapeHtml(v) + '</span>';
  }

  // accountLabel picks the best human-readable label for an
  // account-shaped object. Accepts rows from /api/admin/accounts
  // (identifier), session / /api/me responses (username), and
  // anywhere else we need to render "who". OAuth `sub` identifiers
  // are opaque so display_name always wins when present. One helper
  // so subscriptions dropdown, accounts table, sidebar, and user
  // strip all render the same label for the same account.
  function accountLabel(obj) {
    if (!obj) return '';
    return obj.display_name || obj.identifier || obj.username || '';
  }

  // roleBadgeAttrs returns the HTML attribute fragment for a role
  // badge — `data-role="<role>"`. CSS in admin.css resolves the
  // palette off the attribute (`.badge[data-role="admin"]` etc), so
  // a future role addition is one CSS rule + zero JS branches. The
  // empty string is returned for unknown roles so the badge falls
  // through to the default chip palette without contaminating the
  // markup with `data-role=""`.
  //
  // The tiny shim is the one shared affordance: the accounts table,
  // the sidebar identity strip, and any future role-display surface
  // all build their badge HTML through this helper, so renaming
  // "regular" → "viewer" later is a one-line change.
  function roleBadgeAttrs(role) {
    if (!role) return '';
    return ' data-role="' + escapeHtml(role) + '"';
  }
  // Back-compat alias kept for any caller that still composes badge
  // HTML the old way (class-driven). New call sites prefer
  // roleBadgeAttrs. The only role that mapped to a class before was
  // "admin" → "formidable"; the CSS keeps that selector live too.
  function roleBadgeClass(role) {
    if (role === 'admin') return 'formidable';
    if (role === 'maintainer') return 'maintainer';
    return '';
  }

  // accountLabelHTML returns the same human-readable label as
  // accountLabel, plus the email rendered as a muted secondary span
  // when present. Two accounts that share a display name (e.g. two
  // Microsoft App Registrations for the same human, or a typo'd
  // duplicate) become distinguishable at a glance — the email is the
  // disambiguator the admin actually recognises. Pre-escaped, safe
  // to insert into innerHTML at the call site.
  function accountLabelHTML(obj) {
    const name = accountLabel(obj);
    const email = (obj && obj.email) ? obj.email : '';
    if (!email) return escapeHtml(name);
    return escapeHtml(name) +
      ' <span class="acct-email muted">' + escapeHtml(email) + '</span>';
  }

  // resolveAccount takes a token's stored Username (either
  // "provider:identifier" or a bare local-shorthand string) and
  // returns the matching account row from a supplied list, or null.
  // Centralised so the same (provider, identifier) parsing rules
  // apply everywhere a UI needs to turn a token into a display
  // name — subscriptions grid, /user, repositories sub-chips.
  function resolveAccount(username, accountsList) {
    if (!username || !accountsList) return null;
    let prov = 'local';
    let ident = username;
    const idx = username.indexOf(':');
    if (idx > 0) {
      const head = username.slice(0, idx).toLowerCase();
      if (['local', 'github', 'entra', 'microsoft', 'gateway'].includes(head)) {
        prov = head;
        ident = username.slice(idx + 1);
      }
    }
    return accountsList.find(a => a.provider === prov && a.identifier === ident) || null;
  }

  // accountOption shapes an account row for a GG.select dropdown.
  // Value is the scoped "provider:identifier" the server accepts
  // for token binding; label is human, with provider and (if admin)
  // role suffixed in parens. Admin badge only — regulars are the
  // expected subscription target, no need to flag them.
  function accountOption(a) {
    const suffix = ' (' + a.provider + (a.role === 'admin' ? ' · admin' : '') + ')';
    return {
      value: a.provider + ':' + a.identifier,
      label: accountLabel(a) + suffix,
    };
  }

  // ------------------------------------------------------ token card
  // Shared renderer for a "subscription key" card. Used by the admin
  // /admin/subscriptions page (full header with holder name, bind/
  // edit/revoke actions) AND the user /user landing (read-only, own
  // keys). Callers customise the title/subtitle/actions; the body
  // (repos, abilities, token value, copy button) is fixed so the
  // two pages stay visually aligned.
  //
  // opts: {
  //   title: string                          // required
  //   subtitle: string (HTML) | null         // small muted line under title
  //   leftChips: string (HTML) | null        // extra chips before the repo-count badge (e.g. legacy marker)
  //   actions: [{label, className?, onClick}] | null   // footer buttons; no footer if empty
  // }
  function renderTokenCard(t, opts) {
    opts = opts || {};
    const card = document.createElement('div');
    card.className = 'info-card';
    card.dataset.token = t.token;

    // Subscription keys bind to exactly one repo. Older cards used
    // to render a chip list; now it's a single chip (or a muted
    // "(unbound)" fallback if the server somehow returned an empty
    // string, which should not happen with the post-migration
    // invariant). Legacy tokens without a bound repo fall through
    // to the muted variant instead of crashing the card.
    const repos = t.repo
      ? '<span class="repo-chip">' + escapeHtml(t.repo) + '</span>'
      : '<span class="repo-chip none">(unbound)</span>';
    const abilities = (t.abilities && t.abilities.length)
      ? t.abilities.map(a => '<span class="ability-badge">' + escapeHtml(a) + '</span>').join('')
      : '';

    const subtitleHTML = opts.subtitle ? '<div class="ic-subtitle">' + opts.subtitle + '</div>' : '';
    const leftChipsHTML = opts.leftChips || '';
    // Right-side header chips show only context (legacy badge when
    // applicable) — the repo chip lives in the body row below, so we
    // don't duplicate the repo name in two places. An empty chips
    // container still renders so the flex header preserves spacing.
    const headerChipsHTML = leftChipsHTML;

    const actions = opts.actions || [];
    const actionsHTML = actions.length
      ? '<div class="ic-actions cell-actions">' +
          actions.map((_, i) =>
            '<button type="button" class="small ' + escapeHtml(actions[i].className || '') + '" data-idx="' + i + '">' +
              escapeHtml(actions[i].label) +
            '</button>'
          ).join('') +
        '</div>'
      : '';

    // Token is a password-equivalent secret, so we render it masked
     // by default and expose an eye toggle to reveal it for the
     // seconds it takes to copy. Copy always uses the real value —
     // it ignores the mask state so accidental over-sharing (paste
     // the bullets) can't happen.
    const masked = '•'.repeat(Math.min(48, Math.max(16, t.token.length)));
    card.innerHTML =
      '<div class="ic-header">' +
        '<div class="ic-title-wrap">' +
          '<div class="ic-title" title="' + escapeHtml(t.username || '') + '">' + (opts.titleHTML || escapeHtml(opts.title || '')) + '</div>' +
          subtitleHTML +
        '</div>' +
        (headerChipsHTML ? '<div class="ic-chips">' + headerChipsHTML + '</div>' : '') +
      '</div>' +
      '<div class="ic-chips cell-repos">' + repos + '</div>' +
      (abilities ? '<div class="ic-chips cell-abilities">' + abilities + '</div>' : '') +
      '<div class="token-field" data-revealed="0">' +
        '<code class="token-value token-masked">' + masked + '</code>' +
        '<button type="button" class="icon-btn eye-btn" aria-label="Show key" title="Show key">' +
          eyeIconSVG(false) +
        '</button>' +
        '<button type="button" class="copy-btn">Copy</button>' +
      '</div>' +
      actionsHTML;

    const field = card.querySelector('.token-field');
    const valueEl = field.querySelector('.token-value');
    const eyeBtn = field.querySelector('.eye-btn');
    eyeBtn.addEventListener('click', () => {
      const revealed = field.dataset.revealed === '1';
      const next = !revealed;
      field.dataset.revealed = next ? '1' : '0';
      valueEl.textContent = next ? t.token : masked;
      valueEl.classList.toggle('token-masked', !next);
      eyeBtn.setAttribute('aria-label', next ? 'Hide key' : 'Show key');
      eyeBtn.setAttribute('title', next ? 'Hide key' : 'Show key');
      eyeBtn.innerHTML = eyeIconSVG(next);
    });

    card.querySelector('.copy-btn').addEventListener('click', e => copyToClipboard(t.token, e.currentTarget));
    for (const btn of card.querySelectorAll('.ic-actions button[data-idx]')) {
      const idx = Number(btn.dataset.idx);
      btn.addEventListener('click', () => actions[idx].onClick(card));
    }

    return card;
  }

  // eyeIconSVG returns the inline SVG for a show/hide toggle. Open
  // eye = content currently visible (so click hides); crossed-out
  // eye = content currently hidden (so click reveals). The crossed
  // state is the open eye with a diagonal slash — standard password
  // field affordance.
  function eyeIconSVG(revealed) {
    if (revealed) {
      return '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
        '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>' +
        '<circle cx="12" cy="12" r="3"/>' +
        '<line x1="3" y1="3" x2="21" y2="21"/>' +
      '</svg>';
    }
    return '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>' +
      '<circle cx="12" cy="12" r="3"/>' +
    '</svg>';
  }

  // ---------------------------------------------------------- sidebar
  // Every authenticated admin page carries the same sidebar. Instead
  // of duplicating ~30 lines of HTML in three templates, each page
  // drops an empty `<aside id="admin-sidebar"></aside>` host and
  // calls initSidebar(activeKey, who) on boot. `who` is the session
  // response ({ username, display_name, role }) — display_name is
  // preferred when set so OAuth sessions don't surface raw `sub`
  // claims. activeKey picks which nav link gets `.active`.
  // One nav declaration, each entry carries its own role gate. The
  // sidebar renders only the entries the current user qualifies
  // for — a regular can NEVER be shown an admin destination, even
  // if one gets added later and someone forgets to update the
  // filter. That's the invariant: role lives on the item, not on
  // a parallel array.
  const NAV_ITEMS = [
    { key: 'me',            label: 'My subscriptions',  href: '/user' },
    { key: 'repositories',  label: 'Repositories',      href: '/admin/repositories',  adminOnly: true },
    { key: 'subscriptions', label: 'Subscription keys', href: '/admin/subscriptions', adminOnly: true },
    { key: 'credentials',   label: 'Credentials',       href: '/admin/credentials',   adminOnly: true },
    { key: 'tags',          label: 'Tags',              href: '/admin/tags',          adminOnly: true },
    { key: 'accounts',      label: 'Accounts',          href: '/admin/accounts',      adminOnly: true },
    { key: 'auth',          label: 'Authentication',    href: '/admin/auth',          adminOnly: true },
  ];

  function visibleFor(role, item) {
    return role === 'admin' || !item.adminOnly;
  }

  // View-mode is an admin-only affordance: an admin can browse as if
  // they were a regular user to preview what their teammates see. It
  // never grants or revokes privilege — admin routes still work if the
  // admin navigates there directly — it only controls what the nav
  // and role-gated UI render. Stored per-browser under a local key so
  // the mode outlasts a reload and lets admins keep "regular view"
  // pinned while debugging.
  const VIEW_MODE_KEY = 'gigot.view_mode';
  function getViewMode() {
    const v = GG.core.safeLocalStorageGet(VIEW_MODE_KEY);
    return v === 'regular' ? 'regular' : 'admin';
  }
  function setViewMode(mode) {
    GG.core.safeLocalStorageSet(VIEW_MODE_KEY, mode === 'regular' ? 'regular' : 'admin');
  }

  // effectiveRole applies the view-mode override. A real admin who
  // has flipped to regular view is treated as a regular for all
  // UI-rendering decisions (sidebar nav, brand subtitle, role-gated
  // cards). Non-admins are unaffected — the toggle isn't even shown.
  function effectiveRole(who) {
    const real = (who && typeof who === 'object') ? who.role : '';
    if (real === 'admin' && getViewMode() === 'regular') return 'regular';
    return real;
  }

  function initSidebar(activeKey, who) {
    // Back-compat: older call sites passed a plain username string.
    const label = (typeof who === 'string') ? who : accountLabel(who);
    const realRole = (who && typeof who === 'object') ? who.role : '';
    const viewRole = effectiveRole(who);
    const aside = document.getElementById('admin-sidebar');
    if (!aside) return;
    const navItems = NAV_ITEMS.filter(it => visibleFor(viewRole, it));
    const consoleLabel = viewRole === 'admin' ? 'Admin console' : 'My subscriptions';
    // Server stamps `<meta name="gigot-version" content="vX.Y.Z">` into
    // every admin template; we append it after the brand title so the
    // sidebar matches the title bar and the public landing page. Empty
    // content (tests, unset build) → no suffix at all.
    const versionTag = brandVersionSuffix();
    aside.className = 'sidebar';
    aside.innerHTML =
      '<div class="brand">' +
        '<img class="logo" src="/assets/gigot.png" alt="GiGot">' +
        '<div class="brand-text">' +
          '<h1>GiGot' + versionTag + '</h1>' +
          '<div class="muted">' + escapeHtml(consoleLabel) + '</div>' +
        '</div>' +
      '</div>' +
      '<nav>' +
        navItems.map(n =>
          '<a href="' + n.href + '"' + (n.key === activeKey ? ' class="active"' : '') + '>' + escapeHtml(n.label) + '</a>'
        ).join('') +
        '<div class="spacer"></div>' +
        (viewRole === 'admin' ? '<a href="/swagger/index.html" target="_blank" rel="noopener">API documentation</a>' : '') +
      '</nav>' +
      // Sign out belongs with the identity strip, not with the nav —
      // it's a session verb about WHO you are, not a destination you
      // navigate to. Kebab keeps it from hiding the display name.
      '<div class="me">' +
        '<div class="me-label">' +
          '<span class="me-label-prefix">signed in as</span>' +
          '<div class="me-name-row">' +
            '<strong id="me-name">' + escapeHtml(label) + '</strong>' +
            (realRole
              ? '<span class="badge me-role"' + roleBadgeAttrs(realRole) + '>' +
                  escapeHtml(realRole) + '</span>'
              : '') +
          '</div>' +
        '</div>' +
        '<span class="me-actions row-actions"></span>' +
      '</div>' +
      '<div class="theme-row">' +
        '<span class="theme-label">Light theme</span>' +
        '<span id="theme-toggle-host"></span>' +
      '</div>';

    const toggleHost = document.getElementById('theme-toggle-host');
    toggleHost.innerHTML = GG.toggle_switch.html({
      id: 'theme-toggle',
      ariaLabel: 'Toggle light theme',
    });
    GG.theme.initToggle('theme-toggle');

    // View-mode toggle only exists for real admins. Flipping TO
    // regular view while on an admin-only path (e.g. /admin/accounts)
    // would leave the page's content visible with a regular sidebar —
    // confusing and inconsistent, so we bounce to /user. Flipping
    // BACK TO admin view just re-renders the sidebar; the admin
    // usually wants to stay where they are.
    const viewToggle = realRole === 'admin' ? [{
      label: viewRole === 'admin' ? 'Switch to regular view' : 'Switch to admin mode',
      onClick: () => {
        const next = viewRole === 'admin' ? 'regular' : 'admin';
        setViewMode(next);
        if (next === 'regular' && location.pathname.startsWith('/admin/')) {
          location.href = '/user';
        } else {
          location.reload();
        }
      },
    }] : [];

    GG.row_menu.attach(aside.querySelector('.me-actions'), [
      ...viewToggle,
      { label: 'Sign out', onClick: async () => {
        await api.logout();
        location.href = '/admin';
      }, danger: true },
    ]);
  }

  // ---------------------------------------------------- session guard
  // Each page calls this on boot. If there's no session the user is
  // bounced to the login page; if there is one, we return it to the
  // caller (they typically stash the username for the sidebar).
  async function guardSession() {
    const who = await api.session();
    if (!who || !who.username) {
      location.href = '/admin';
      return null;
    }
    return who;
  }

  // ---------------------------------------------------- copyToClipboard
  // Subscription-key cards use this for the Copy button; lives here so
  // the credentials page (future "copy credential name" button, etc.)
  // can share it without a copy-paste.
  async function copyToClipboard(text, btn) {
    try {
      await navigator.clipboard.writeText(text);
      const prev = btn.textContent;
      btn.textContent = 'Copied';
      btn.classList.add('ok');
      setTimeout(() => { btn.textContent = prev; btn.classList.remove('ok'); }, 1200);
    } catch {
      // Clipboard API can be blocked in insecure contexts — fall back
      // to selecting the adjacent <code> so the user can ⌘/Ctrl-C.
      const code = btn.parentElement.querySelector('code');
      if (code) {
        const range = document.createRange();
        range.selectNodeContents(code);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
      }
    }
  }

  window.Admin = {
    api,
    escapeHtml, shortSha,
    accountLabel, accountLabelHTML, accountOption, resolveAccount, roleBadgeClass, roleBadgeAttrs,
    renderTokenCard,
    initSidebar, guardSession,
    copyToClipboard,
  };
})();
