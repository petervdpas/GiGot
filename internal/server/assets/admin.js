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

let repoCache = [];

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function renderRepoPicker(container, selected) {
  const sel = new Set(selected || []);
  container.innerHTML = '';
  if (repoCache.length === 0) {
    container.innerHTML = '<span class="muted">No repos exist yet — create one above.</span>';
    return;
  }
  for (const name of repoCache) {
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

async function refreshRepos() {
  try {
    const data = await api.listRepos();
    repoCache = data.repos.map(r => r.name);
    document.getElementById('repo-count').textContent = data.count;
    const list = document.getElementById('repo-list');
    list.replaceChildren(...repoCache.map(name => {
      const li = document.createElement('li');
      li.innerHTML =
        '<code>' + escapeHtml(name) + '</code> ' +
        '<button class="small danger" data-repo="' + escapeHtml(name) + '">Delete</button>';
      return li;
    }));
    list.querySelectorAll('button.danger').forEach(b => {
      b.addEventListener('click', async () => {
        if (!confirm('Delete repo "' + b.dataset.repo + '"? This is destructive.')) return;
        try {
          await api.deleteRepo(b.dataset.repo);
          await refreshRepos();
          await refreshTokens();
        } catch (e) {
          alert(e.message);
        }
      });
    });
    renderRepoPicker(document.getElementById('issue-repos'), []);
  } catch (e) {
    console.error(e);
  }
}

function renderTokenRow(t) {
  const tr = document.createElement('tr');
  tr.dataset.token = t.token;

  const repos = (t.repos && t.repos.length)
    ? t.repos.map(r => '<span class="repo-chip">' + escapeHtml(r) + '</span>').join(' ')
    : '<span class="repo-chip none">no repos</span>';

  tr.innerHTML =
    '<td>' + escapeHtml(t.username) + '</td>' +
    '<td class="cell-repos"><span class="repo-list">' + repos + '</span></td>' +
    '<td><code>' + escapeHtml(t.token) + '</code></td>' +
    '<td class="row cell-actions">' +
      '<button class="small secondary edit-btn">Edit access</button> ' +
      '<button class="small danger revoke-btn">Revoke</button>' +
    '</td>';

  tr.querySelector('.revoke-btn').addEventListener('click', async () => {
    if (!confirm('Revoke this key?')) return;
    await api.revokeToken(t.token);
    refreshTokens();
  });

  tr.querySelector('.edit-btn').addEventListener('click', () => enterEditMode(tr, t));
  return tr;
}

function enterEditMode(tr, t) {
  const cellRepos = tr.querySelector('.cell-repos');
  const cellActions = tr.querySelector('.cell-actions');

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
    const fresh = renderTokenRow(t);
    tr.replaceWith(fresh);
  });
}

async function refreshTokens() {
  try {
    const data = await api.listTokens();
    document.getElementById('count').textContent = data.count;
    const tbody = document.getElementById('token-rows');
    tbody.replaceChildren(...data.tokens.map(renderTokenRow));
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
