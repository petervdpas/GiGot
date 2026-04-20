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
    async issueToken(username, repos, abilities) {
      const r = await fetch('/api/admin/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ username, repos, abilities }),
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

  // ---------------------------------------------------------- sidebar
  // Every authenticated admin page carries the same sidebar. Instead
  // of duplicating ~30 lines of HTML in three templates, each page
  // drops an empty `<aside id="admin-sidebar"></aside>` host and
  // calls initSidebar(activeKey, username) on boot. activeKey picks
  // which nav link gets `.active`. Nav links are plain <a href>s now
  // (no JS panel-switching) since each section lives on its own page.
  function initSidebar(activeKey, username) {
    const aside = document.getElementById('admin-sidebar');
    if (!aside) return;
    const navItems = [
      { key: 'repositories', label: 'Repositories',     href: '/admin/repositories' },
      { key: 'subscriptions', label: 'Subscription keys', href: '/admin/subscriptions' },
      { key: 'credentials', label: 'Credentials',      href: '/admin/credentials' },
      { key: 'accounts',    label: 'Accounts',         href: '/admin/accounts' },
      { key: 'auth',        label: 'Authentication',   href: '/admin/auth' },
    ];
    aside.className = 'sidebar';
    aside.innerHTML =
      '<div class="brand">' +
        '<img class="logo" src="/assets/gigot.png" alt="GiGot">' +
        '<div class="brand-text">' +
          '<h1>GiGot</h1>' +
          '<div class="muted">Admin console</div>' +
        '</div>' +
      '</div>' +
      '<nav>' +
        navItems.map(n =>
          '<a href="' + n.href + '"' + (n.key === activeKey ? ' class="active"' : '') + '>' + escapeHtml(n.label) + '</a>'
        ).join('') +
        '<div class="spacer"></div>' +
        '<a href="/swagger/index.html" target="_blank" rel="noopener">API documentation</a>' +
        '<a id="logout">Sign out</a>' +
      '</nav>' +
      '<div class="me">' +
        'signed in as<strong id="me-name">' + escapeHtml(username || '') + '</strong>' +
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

    document.getElementById('logout').addEventListener('click', async () => {
      await api.logout();
      location.href = '/admin';
    });
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

  window.Admin = { api, escapeHtml, shortSha, initSidebar, guardSession, copyToClipboard };
})();
