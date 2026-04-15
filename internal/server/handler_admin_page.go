package server

import (
	"html/template"
	"net/http"
)

var adminPageTmpl = template.Must(template.New("admin").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>GiGot Admin</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
         background: #0d1117; color: #c9d1d9; min-height: 100vh; padding: 2rem; }
  header { display: flex; justify-content: space-between; align-items: center;
           border-bottom: 1px solid #30363d; padding-bottom: 1rem; margin-bottom: 1.5rem; }
  h1 { color: #f0f6fc; font-size: 1.6rem; }
  h2 { color: #f0f6fc; font-size: 1.1rem; margin: 1.25rem 0 0.75rem; }
  button { background: #238636; border: 0; color: white; padding: 0.5rem 1rem;
           border-radius: 6px; cursor: pointer; font-size: 0.9rem; }
  button.secondary { background: #21262d; border: 1px solid #30363d; }
  button.danger { background: #da3633; }
  button.small { padding: 0.3rem 0.6rem; font-size: 0.8rem; }
  input { background: #0d1117; border: 1px solid #30363d; color: #c9d1d9;
          padding: 0.5rem 0.75rem; border-radius: 6px; font-size: 0.9rem; min-width: 220px; }
  input:focus { outline: none; border-color: #58a6ff; }
  .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px;
          padding: 1.5rem; margin-bottom: 1rem; }
  .row { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
  .muted { color: #8b949e; font-size: 0.85rem; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid #21262d;
           vertical-align: top; }
  th { color: #8b949e; font-weight: 500; font-size: 0.85rem; text-transform: uppercase; }
  code { background: #0d1117; padding: 2px 6px; border-radius: 4px; font-size: 0.85rem;
         word-break: break-all; }
  .error { color: #f85149; font-size: 0.9rem; margin-top: 0.5rem; }
  .success { color: #3fb950; font-size: 0.9rem; margin-top: 0.5rem; }
  .hidden { display: none; }
  form { display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: center; }
  .login-wrap { max-width: 380px; margin: 4rem auto; }
  .repo-picker { display: flex; flex-direction: column; gap: 0.25rem;
                 max-height: 140px; overflow-y: auto; padding: 0.5rem;
                 border: 1px solid #30363d; border-radius: 6px; min-width: 260px; }
  .repo-picker label { font-size: 0.9rem; color: #c9d1d9; cursor: pointer; }
  .repo-picker input[type=checkbox] { min-width: 0; margin-right: 0.4rem; }
  .repo-list { display: inline-flex; flex-wrap: wrap; gap: 0.25rem; }
  .repo-chip { background: #21262d; border: 1px solid #30363d; border-radius: 12px;
               padding: 2px 8px; font-size: 0.8rem; }
  .repo-chip.none { color: #8b949e; font-style: italic; }
  a { color: #58a6ff; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .footer-link { display: block; text-align: center; margin-top: 1rem;
                 font-size: 0.85rem; color: #8b949e; }
</style>
</head>
<body>

<div id="login" class="login-wrap card hidden">
  <h2>Admin login</h2>
  <form id="login-form">
    <input name="username" placeholder="Username" autofocus required>
    <input name="password" type="password" placeholder="Password" required>
    <button type="submit">Sign in</button>
    <div id="login-error" class="error"></div>
  </form>
  <a class="footer-link" href="/swagger/index.html">API documentation</a>
</div>

<div id="app" class="hidden">
  <header>
    <h1>GiGot Admin</h1>
    <div class="row">
      <a href="/swagger/index.html" class="muted">API docs</a>
      <span class="muted">signed in as <strong id="me-name"></strong></span>
      <button class="secondary" id="logout">Sign out</button>
    </div>
  </header>

  <div class="card">
    <h2>Repositories (<span id="repo-count">0</span>)</h2>
    <form id="create-repo-form" class="row">
      <input name="name" placeholder="New repo name" required>
      <button type="submit">Create repo</button>
    </form>
    <div id="repo-msg" class="muted"></div>
    <ul id="repo-list" style="margin-top:0.75rem; list-style:none;"></ul>
  </div>

  <div class="card">
    <h2>Issue subscription key</h2>
    <form id="issue-form" class="row" style="align-items:flex-start;">
      <input name="username" placeholder="Client username" required>
      <div>
        <div class="muted" style="margin-bottom:0.25rem;">Repos this key can access:</div>
        <div id="issue-repos" class="repo-picker"></div>
      </div>
      <button type="submit">Issue key</button>
    </form>
    <div id="issue-msg" class="success"></div>
  </div>

  <div class="card">
    <h2>Active subscription keys (<span id="count">0</span>)</h2>
    <table>
      <thead><tr><th>Username</th><th>Repos</th><th>Token</th><th></th></tr></thead>
      <tbody id="token-rows"></tbody>
    </table>
  </div>
</div>

<script>
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
  async createRepo(name) {
    const r = await fetch('/api/repos', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ name }),
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
    const id = 'repo-' + container.id + '-' + name;
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
      li.style.padding = '0.3rem 0';
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
  status.className = 'muted';
  status.style.marginLeft = '0.5rem';
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
    // Re-render just this row from the cached token data.
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
  try {
    await api.createRepo(f.name.value);
    f.reset();
    await refreshRepos();
  } catch (ex) {
    msg.textContent = ex.message;
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
})();
</script>
</body>
</html>`))

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminPageTmpl.Execute(w, nil)
}
