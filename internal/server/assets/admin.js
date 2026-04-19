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
};

const loginView = document.getElementById('login');
const appView = document.getElementById('app');

// repoInfoCache carries the full RepoInfo objects returned by the list
// endpoint so renderers that need more than just the name (cards,
// pickers) don't have to re-fetch. repoNames() exists because the
// picker and the issue form still just want the name set.
let repoInfoCache = [];
function repoNames() { return repoInfoCache.map(r => r.name); }

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
  card.className = 'info-card';

  const badges = [];
  if (r.has_formidable) badges.push('<span class="badge formidable" title="Scaffolded as a Formidable context">Formidable</span>');
  if (r.empty) badges.push('<span class="badge empty" title="No commits yet — nothing has been pushed to this repo">empty</span>');

  // Stats always show so a card never renders with just a name and a
  // delete button. An empty repo displays COMMITS 0 explicitly — no
  // HEAD/Branch lines (there is no HEAD), but the zero-commits stat
  // makes the state unambiguous.
  const stats = [];
  const commitLabel = r.commits === 1 ? 'commit' : 'commits';
  stats.push('<span><span class="stat-label">' + commitLabel + '</span><span class="stat-value">' + r.commits + '</span></span>');
  if (!r.empty) {
    stats.push('<span><span class="stat-label">HEAD</span><span class="stat-value">' + escapeHtml(shortSha(r.head)) + '</span></span>');
    if (r.default_branch) {
      stats.push('<span><span class="stat-label">Branch</span><span class="stat-value">' + escapeHtml(r.default_branch) + '</span></span>');
    }
  }
  stats.push('<span><span class="stat-label">Destinations</span><span class="stat-value">' + r.destination_count + '</span></span>');

  card.innerHTML =
    '<div class="ic-header">' +
      '<div class="ic-title">' + escapeHtml(r.name) + '</div>' +
      '<div class="ic-chips">' + badges.join('') + '</div>' +
    '</div>' +
    '<div class="ic-stats">' + stats.join('') + '</div>' +
    '<div class="ic-actions">' +
      '<button class="small danger delete-btn">Delete</button>' +
    '</div>';

  card.querySelector('.delete-btn').addEventListener('click', async () => {
    if (!confirm('Delete repo "' + r.name + '"? This is destructive — the bare repo and any attached destinations are dropped.')) return;
    try {
      await api.deleteRepo(r.name);
      await refreshRepos();
      await refreshTokens();
    } catch (e) {
      alert(e.message);
    }
  });

  return card;
}

async function refreshRepos() {
  try {
    const data = await api.listRepos();
    repoInfoCache = data.repos || [];
    document.getElementById('repo-count').textContent = data.count;
    const grid = document.getElementById('repo-grid');
    const empty = document.getElementById('repo-empty');
    grid.replaceChildren(...repoInfoCache.map(renderRepoCard));
    empty.classList.toggle('hidden', repoInfoCache.length !== 0);
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

async function refreshTokens() {
  try {
    const data = await api.listTokens();
    document.getElementById('count').textContent = data.count;
    const grid = document.getElementById('token-grid');
    const empty = document.getElementById('token-empty');
    const tokens = data.tokens || [];
    grid.replaceChildren(...tokens.map(renderTokenCard));
    empty.classList.toggle('hidden', tokens.length !== 0);
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
    refreshRepos().then(refreshTokens);
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
