const api = {
  async session() {
    const r = await fetch('/api/admin/session', { credentials: 'same-origin' });
    return r.ok ? r.json() : null;
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
    // Only send scaffold_formidable when the admin explicitly ticked the
    // box. Omitting it lets the server apply its configured default
    // (server.formidable_first, see design doc §2.7) — sending a hard
    // false would override the default and silently defeat a Formidable-
    // first deployment.
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
  async issueToken(username, repos) {
    const r = await fetch('/api/admin/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ username, repos }),
    });
    if (!r.ok) throw new Error((await r.json()).error || 'issue failed');
    return r.json();
  },
  async updateTokenRepos(token, repos) {
    const r = await fetch('/api/admin/tokens', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ token, repos }),
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
  async listCredentials() {
    const r = await fetch('/api/admin/credentials', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('list credentials failed');
    return r.json();
  },
};

const loginView = document.getElementById('login');
const appView = document.getElementById('app');

// repoInfoCache carries the full RepoInfo objects returned by the list
// endpoint so renderers that need more than just the name (cards,
// pickers) don't have to re-fetch. repoNames() exists because the
// picker and the issue form still just want the name set.
let repoInfoCache = [];
// tokensCache and credentialsCache are kept in sync with repoInfoCache so
// the relational repo card can render subscriptions (tokens granting
// access to this repo) and the destination's credential dropdown without
// extra round-trips per re-render.
let tokensCache = [];
let credentialsCache = [];
// destinationsByRepo maps repo name → Destination (first one; the UI
// treats destinations as 1:1 per repo per the diagram, even though the
// data model allows N).
let destinationsByRepo = {};
// pendingKeysRepo is set when a repo card asks to jump to the keys panel
// with itself pre-selected in the issue form. The keys panel picks this
// up on activation and clears it.
let pendingKeysRepo = '';
function repoNames() { return repoInfoCache.map(r => r.name); }
function subscriptionsForRepo(name) {
  return tokensCache.filter(t => (t.repos || []).includes(name));
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function shortSha(sha) { return sha ? sha.slice(0, 7) : ''; }

function renderRepoPicker(container, selected) {
  const sel = new Set(selected || []);
  container.innerHTML = '';
  const names = repoNames();
  if (names.length === 0) {
    container.innerHTML = '<span class="muted">No repos exist yet — create one above.</span>';
    return;
  }
  for (const name of names) {
    const label = document.createElement('label');
    label.innerHTML =
      '<input type="checkbox" value="' + escapeHtml(name) + '"' +
      (sel.has(name) ? ' checked' : '') + '> ' + escapeHtml(name);
    container.appendChild(label);
  }
}

function selectedReposFromPicker(container) {
  return Array.from(container.querySelectorAll('input[type=checkbox]:checked'))
    .map(cb => cb.value);
}

function renderRepoCard(r) {
  const card = document.createElement('div');
  card.className = 'info-card repo-card';
  card.dataset.repo = r.name;

  const badges = [];
  if (r.has_formidable) badges.push('<span class="badge formidable" title="Scaffolded as a Formidable context">Formidable</span>');
  if (r.empty) badges.push('<span class="badge empty" title="No commits yet — nothing has been pushed to this repo">empty</span>');

  const stats = [];
  const commitLabel = r.commits === 1 ? 'commit' : 'commits';
  stats.push('<span><span class="stat-label">' + commitLabel + '</span><span class="stat-value">' + r.commits + '</span></span>');
  if (!r.empty) {
    stats.push('<span><span class="stat-label">HEAD</span><span class="stat-value">' + escapeHtml(shortSha(r.head)) + '</span></span>');
    if (r.default_branch) {
      stats.push('<span><span class="stat-label">Branch</span><span class="stat-value">' + escapeHtml(r.default_branch) + '</span></span>');
    }
  }

  card.innerHTML =
    '<div class="ic-header">' +
      '<div class="ic-title">' + escapeHtml(r.name) + '</div>' +
      '<div class="ic-chips">' + badges.join('') + '</div>' +
    '</div>' +
    '<div class="ic-stats">' + stats.join('') + '</div>' +
    '<div class="ic-section" data-section="subs"></div>' +
    '<div class="ic-section" data-section="dest"></div>' +
    '<div class="ic-actions">' +
      '<button class="small danger delete-btn">Delete</button>' +
    '</div>';

  renderSubscriptionsSection(card.querySelector('[data-section="subs"]'), r.name);
  renderDestinationSection(card.querySelector('[data-section="dest"]'), r.name);

  card.querySelector('.delete-btn').addEventListener('click', async () => {
    if (!confirm('Delete repo "' + r.name + '"? This is destructive — the bare repo and any attached destinations are dropped.')) return;
    try {
      await api.deleteRepo(r.name);
      await refreshRepos();
    } catch (e) {
      alert(e.message);
    }
  });

  return card;
}

function renderSubscriptionsSection(container, repoName) {
  const subs = subscriptionsForRepo(repoName);
  const header = '<div class="ic-section-head">' +
    '<span class="ic-section-title">Subscriptions</span>' +
    '<span class="muted">(' + subs.length + ')</span>' +
    '<button type="button" class="small secondary issue-key-btn">+ Issue key</button>' +
    '</div>';
  const body = subs.length === 0
    ? '<div class="muted ic-section-empty">No subscription keys grant access to this repo.</div>'
    : '<div class="sub-chips">' +
        subs.map(s => '<span class="sub-chip">' + escapeHtml(s.username) + '</span>').join('') +
      '</div>';
  container.innerHTML = header + body;
  container.querySelector('.issue-key-btn').addEventListener('click', () => {
    pendingKeysRepo = repoName;
    activatePanel('keys');
  });
}

function renderDestinationSection(container, repoName) {
  const dest = destinationsByRepo[repoName] || null;
  const header = '<div class="ic-section-head">' +
    '<span class="ic-section-title">Mirror destination</span>' +
    (dest ? '' : '<button type="button" class="small secondary add-dest-btn">+ Add</button>') +
    '</div>';
  if (!dest) {
    container.innerHTML = header +
      '<div class="muted ic-section-empty">Not mirrored. Add a destination to push this repo to an external git remote.</div>';
    container.querySelector('.add-dest-btn').addEventListener('click', () => {
      renderDestinationEditor(container, repoName, null);
    });
    return;
  }
  const credPill = dest.credential_name
    ? '<span class="cred-pill">' + escapeHtml(dest.credential_name) + '</span>'
    : '<span class="cred-pill missing">(no credential)</span>';
  const enabledTag = dest.enabled
    ? '<span class="badge formidable">enabled</span>'
    : '<span class="badge empty">disabled</span>';
  container.innerHTML = header +
    '<div class="dest-row">' +
      '<div class="dest-url"><span class="stat-label">URL</span> <code>' + escapeHtml(dest.url) + '</code></div>' +
      '<div class="dest-meta">' +
        '<span class="stat-label">Credential</span> ' + credPill + ' ' + enabledTag +
      '</div>' +
      '<div class="dest-actions">' +
        '<button type="button" class="small secondary edit-dest-btn">Edit</button>' +
        '<button type="button" class="small danger remove-dest-btn">Remove</button>' +
      '</div>' +
    '</div>';
  container.querySelector('.edit-dest-btn').addEventListener('click', () => {
    renderDestinationEditor(container, repoName, dest);
  });
  container.querySelector('.remove-dest-btn').addEventListener('click', async () => {
    if (!confirm('Remove mirror destination from "' + repoName + '"?')) return;
    try {
      await api.deleteDestination(repoName, dest.id);
      await refreshRepos();
    } catch (e) {
      alert(e.message);
    }
  });
}

function renderDestinationEditor(container, repoName, existing) {
  const isEdit = !!existing;
  const credOptions = credentialsCache.length === 0
    ? '<option value="">(no credentials in the vault — add one first)</option>'
    : ['<option value="">Select a credential…</option>']
        .concat(credentialsCache.map(c => {
          const sel = existing && existing.credential_name === c.name ? ' selected' : '';
          return '<option value="' + escapeHtml(c.name) + '"' + sel + '>' + escapeHtml(c.name) + '</option>';
        }))
        .join('');
  const urlVal = existing ? escapeHtml(existing.url) : '';
  const enabledChecked = !existing || existing.enabled ? 'checked' : '';
  container.innerHTML =
    '<div class="ic-section-head">' +
      '<span class="ic-section-title">' + (isEdit ? 'Edit mirror destination' : 'Add mirror destination') + '</span>' +
    '</div>' +
    '<form class="dest-form">' +
      '<label class="dest-field"><span class="stat-label">URL</span>' +
        '<input type="text" name="url" value="' + urlVal + '" placeholder="https://github.com/org/repo.git" required>' +
      '</label>' +
      '<label class="dest-field"><span class="stat-label">Credential</span>' +
        '<select name="credential_name" required>' + credOptions + '</select>' +
      '</label>' +
      '<label class="dest-field inline">' +
        '<input type="checkbox" name="enabled" ' + enabledChecked + '> Enable mirror-sync to this destination' +
      '</label>' +
      '<div class="dest-actions">' +
        '<button type="submit" class="small">' + (isEdit ? 'Save' : 'Add') + '</button>' +
        '<button type="button" class="small secondary cancel-btn">Cancel</button>' +
        '<span class="dest-err error"></span>' +
      '</div>' +
    '</form>';
  const form = container.querySelector('.dest-form');
  const err = form.querySelector('.dest-err');
  form.querySelector('.cancel-btn').addEventListener('click', () => {
    renderDestinationSection(container, repoName);
  });
  form.addEventListener('submit', async e => {
    e.preventDefault();
    err.textContent = '';
    const body = {
      url: form.url.value.trim(),
      credential_name: form.credential_name.value,
      enabled: form.enabled.checked,
    };
    try {
      if (isEdit) {
        await api.updateDestination(repoName, existing.id, body);
      } else {
        await api.createDestination(repoName, body);
      }
      await refreshRepos();
    } catch (ex) {
      err.textContent = ex.message;
    }
  });
}

async function refreshRepos() {
  try {
    const [repoData, tokenData, credData] = await Promise.all([
      api.listRepos(),
      api.listTokens().catch(() => ({ tokens: [], count: 0 })),
      api.listCredentials().catch(() => ({ credentials: [], count: 0 })),
    ]);
    repoInfoCache = repoData.repos || [];
    tokensCache = tokenData.tokens || [];
    credentialsCache = credData.credentials || [];

    // Destinations are admin-scoped and per-repo — one fetch per repo is
    // fine at the size of admin workloads, and keeps the public /api/repos
    // response free of admin-only fields.
    const destEntries = await Promise.all(repoInfoCache.map(async r => {
      try {
        const d = await api.listDestinations(r.name);
        return [r.name, (d.destinations || [])[0] || null];
      } catch {
        return [r.name, null];
      }
    }));
    destinationsByRepo = Object.fromEntries(destEntries);

    document.getElementById('repo-count').textContent = repoData.count;
    const grid = document.getElementById('repo-grid');
    const empty = document.getElementById('repo-empty');
    grid.replaceChildren(...repoInfoCache.map(renderRepoCard));
    empty.classList.toggle('hidden', repoInfoCache.length !== 0);

    // Keep the global keys-panel views in sync with what we just fetched
    // (the repo card is the relational view; the keys panel is the global
    // list). refreshTokens() re-renders using tokensCache.
    renderTokensGrid();
    renderRepoPicker(document.getElementById('issue-repos'), []);
  } catch (e) {
    console.error(e);
  }
}

async function copyToClipboard(text, btn) {
  try {
    await navigator.clipboard.writeText(text);
    const prev = btn.textContent;
    btn.textContent = 'Copied';
    btn.classList.add('ok');
    setTimeout(() => {
      btn.textContent = prev;
      btn.classList.remove('ok');
    }, 1200);
  } catch {
    // Clipboard API can be blocked in insecure contexts — fall back to
    // selecting the code so the user can ⌘/Ctrl-C manually.
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

function renderTokenCard(t) {
  const card = document.createElement('div');
  card.className = 'info-card';
  card.dataset.token = t.token;

  const repos = (t.repos && t.repos.length)
    ? t.repos.map(r => '<span class="repo-chip">' + escapeHtml(r) + '</span>').join('')
    : '<span class="repo-chip none">no repos</span>';

  card.innerHTML =
    '<div class="ic-header">' +
      '<div class="ic-title">' + escapeHtml(t.username) + '</div>' +
      '<div class="ic-chips"><span class="badge formidable">' + (t.repos ? t.repos.length : 0) + ' ' +
        ((t.repos && t.repos.length === 1) ? 'repo' : 'repos') + '</span></div>' +
    '</div>' +
    '<div class="ic-chips cell-repos">' + repos + '</div>' +
    '<div class="token-field">' +
      '<code class="token-value">' + escapeHtml(t.token) + '</code>' +
      '<button type="button" class="copy-btn">Copy</button>' +
    '</div>' +
    '<div class="ic-actions cell-actions">' +
      '<button class="small secondary edit-btn">Edit access</button>' +
      '<button class="small danger revoke-btn">Revoke</button>' +
    '</div>';

  card.querySelector('.copy-btn').addEventListener('click', e => copyToClipboard(t.token, e.currentTarget));
  card.querySelector('.revoke-btn').addEventListener('click', async () => {
    if (!confirm('Revoke this key?')) return;
    await api.revokeToken(t.token);
    refreshTokens();
  });
  card.querySelector('.edit-btn').addEventListener('click', () => enterEditMode(card, t));
  return card;
}

function enterEditMode(card, t) {
  const cellRepos = card.querySelector('.cell-repos');
  const cellActions = card.querySelector('.cell-actions');

  const picker = document.createElement('div');
  picker.className = 'repo-picker';
  renderRepoPicker(picker, t.repos || []);
  cellRepos.replaceChildren(picker);

  const save = document.createElement('button');
  save.className = 'small';
  save.textContent = 'Save';
  const cancel = document.createElement('button');
  cancel.className = 'small secondary';
  cancel.textContent = 'Cancel';
  const status = document.createElement('span');
  status.className = 'muted edit-status';
  cellActions.replaceChildren(save, cancel, status);

  save.addEventListener('click', async () => {
    const repos = selectedReposFromPicker(picker);
    save.disabled = true;
    try {
      await api.updateTokenRepos(t.token, repos);
      refreshTokens();
    } catch (e) {
      save.disabled = false;
      status.textContent = e.message;
      status.className = 'error';
    }
  });

  cancel.addEventListener('click', () => {
    const fresh = renderTokenCard(t);
    card.replaceWith(fresh);
  });
}

// renderTokensGrid paints the keys panel from the module-level tokensCache.
// Used when refreshRepos already fetched tokens — avoids a second fetch.
function renderTokensGrid() {
  document.getElementById('count').textContent = tokensCache.length;
  const grid = document.getElementById('token-grid');
  const empty = document.getElementById('token-empty');
  grid.replaceChildren(...tokensCache.map(renderTokenCard));
  empty.classList.toggle('hidden', tokensCache.length !== 0);
}

// refreshTokens re-fetches tokens from the server and re-renders. Called
// after token-mutating actions (issue, revoke, edit) so the view is
// authoritative; refreshRepos also updates tokensCache but goes the long
// way (repos + destinations too).
async function refreshTokens() {
  try {
    const data = await api.listTokens();
    tokensCache = data.tokens || [];
    renderTokensGrid();
    // Repo cards show subscriptions derived from tokensCache, so any
    // token change has to re-paint them too.
    const grid = document.getElementById('repo-grid');
    if (grid) grid.replaceChildren(...repoInfoCache.map(renderRepoCard));
  } catch (e) {
    console.error(e);
  }
}

function show(who) {
  const loggedIn = who && who.username;
  loginView.classList.toggle('hidden', !!loggedIn);
  appView.classList.toggle('hidden', !loggedIn);
  if (loggedIn) {
    document.getElementById('me-name').textContent = who.username;
    // refreshRepos already fetches tokens + credentials + destinations
    // and paints both grids, so no chained refreshTokens needed here.
    refreshRepos();
  }
}

function activatePanel(name) {
  document.querySelectorAll('.panel').forEach(p => {
    p.classList.toggle('active', p.id === 'panel-' + name);
  });
  document.querySelectorAll('.sidebar nav a[data-panel]').forEach(a => {
    a.classList.toggle('active', a.dataset.panel === name);
  });
  if (location.hash !== '#' + name) {
    history.replaceState(null, '', '#' + name);
  }
  // Honor "+ Issue key" deep-link from a repo card: re-render the picker
  // with that repo pre-selected, focus the username input, then clear
  // the pending flag so a subsequent plain panel activation is not sticky.
  if (name === 'keys' && pendingKeysRepo) {
    renderRepoPicker(document.getElementById('issue-repos'), [pendingKeysRepo]);
    const u = document.querySelector('#issue-form [name="username"]');
    if (u) u.focus();
    pendingKeysRepo = '';
  }
}

document.querySelectorAll('.sidebar nav a[data-panel]').forEach(a => {
  a.addEventListener('click', e => {
    e.preventDefault();
    activatePanel(a.dataset.panel);
  });
});

window.addEventListener('hashchange', () => {
  const name = (location.hash || '#repos').slice(1);
  if (document.getElementById('panel-' + name)) activatePanel(name);
});

document.getElementById('login-form').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const err = document.getElementById('login-error');
  err.textContent = '';
  try {
    const who = await api.login(f.username.value, f.password.value);
    show(who);
  } catch (ex) {
    err.textContent = ex.message;
  }
});

document.getElementById('logout').addEventListener('click', async () => {
  await api.logout();
  show(null);
});

document.getElementById('create-repo-form').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const msg = document.getElementById('repo-msg');
  msg.textContent = '';
  msg.className = 'muted';
  try {
    await api.createRepo(f.name.value, f.scaffold.checked, f.source_url.value.trim());
    f.reset();
    await refreshRepos();
  } catch (ex) {
    msg.textContent = ex.message;
    msg.className = 'error';
  }
});

document.getElementById('issue-form').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const msg = document.getElementById('issue-msg');
  msg.textContent = '';
  const repos = selectedReposFromPicker(document.getElementById('issue-repos'));
  try {
    const t = await api.issueToken(f.username.value, repos);
    msg.textContent = 'Issued: ' + t.token;
    f.reset();
    renderRepoPicker(document.getElementById('issue-repos'), []);
    refreshTokens();
  } catch (ex) {
    msg.textContent = ex.message;
    msg.className = 'error';
  }
});

(async () => {
  const who = await api.session();
  show(who);
  if (who && who.username) {
    const initial = (location.hash || '#repos').slice(1);
    if (document.getElementById('panel-' + initial)) activatePanel(initial);
  }
})();
