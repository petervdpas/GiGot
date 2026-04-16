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
         background: #0d1117; color: #c9d1d9; min-height: 100vh; }
  h1 { color: #f0f6fc; font-size: 1.4rem; }
  h2 { color: #f0f6fc; font-size: 1.1rem; margin-bottom: 0.75rem; }
  button { background: #238636; border: 0; color: white; padding: 0.5rem 1rem;
           border-radius: 6px; cursor: pointer; font-size: 0.9rem; }
  button.secondary { background: #21262d; border: 1px solid #30363d; color: #c9d1d9; }
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
  .login-wrap { max-width: 380px; margin: 4rem auto; padding: 0 1rem; }
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

  /* Layout: left sidebar + main content. */
  .shell { display: flex; min-height: 100vh; }
  .sidebar { width: 240px; flex-shrink: 0; background: #0a0d12;
             border-right: 1px solid #30363d; display: flex; flex-direction: column;
             padding: 1.25rem 0; position: sticky; top: 0; height: 100vh; }
  .sidebar .brand { padding: 0 1.25rem 1.25rem; border-bottom: 1px solid #21262d;
                    margin-bottom: 0.5rem; }
  .sidebar nav { display: flex; flex-direction: column; flex: 1; gap: 2px;
                 padding: 0.5rem 0.5rem; }
  .sidebar nav a { display: flex; align-items: center; gap: 0.5rem;
                   padding: 0.6rem 0.75rem; border-radius: 6px; color: #c9d1d9;
                   font-size: 0.9rem; cursor: pointer; }
  .sidebar nav a:hover { background: #161b22; text-decoration: none; }
  .sidebar nav a.active { background: #1f6feb33; color: #f0f6fc; }
  .sidebar nav .spacer { flex: 1; }
  .sidebar .me { padding: 1rem 1.25rem; border-top: 1px solid #21262d;
                 font-size: 0.85rem; color: #8b949e; }
  .sidebar .me strong { color: #c9d1d9; display: block; }
  .main { flex: 1; padding: 2rem; overflow-x: auto; }
  .page-header { display: flex; justify-content: space-between; align-items: baseline;
                 margin-bottom: 1.5rem; }
  .panel { display: none; }
  .panel.active { display: block; }
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
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <h1>GiGot</h1>
        <div class="muted">Admin console</div>
      </div>
      <nav>
        <a data-panel="repos" class="active">Repositories</a>
        <a data-panel="keys">Subscription keys</a>
        <div class="spacer"></div>
        <a href="/swagger/index.html" target="_blank" rel="noopener">API documentation</a>
        <a id="logout">Sign out</a>
      </nav>
      <div class="me">
        signed in as
        <strong id="me-name"></strong>
      </div>
    </aside>

    <main class="main">
      <div id="panel-repos" class="panel active">
        <div class="page-header">
          <h1>Repositories <span class="muted">(<span id="repo-count">0</span>)</span></h1>
        </div>
        <div class="card">
          <h2>Create repository</h2>
          <form id="create-repo-form" class="row">
            <input name="name" placeholder="New repo name" required>
            <input name="source_url" placeholder="Clone from URL (optional)" style="min-width:320px;">
            <label class="muted" style="display:flex; align-items:center; gap:0.3rem;">
              <input type="checkbox" name="scaffold" style="min-width:0;">
              Scaffold as Formidable context
            </label>
            <button type="submit">Create repo</button>
          </form>
          <div class="muted" style="margin-top:0.5rem; font-size:0.8rem;">
            Leave URL empty to create an empty bare repo. URL + scaffold are mutually exclusive.
          </div>
          <div id="repo-msg" class="muted"></div>
        </div>
        <div class="card">
          <h2>Existing repositories</h2>
          <ul id="repo-list" style="list-style:none;"></ul>
        </div>
      </div>

      <div id="panel-keys" class="panel">
        <div class="page-header">
          <h1>Subscription keys <span class="muted">(<span id="count">0</span>)</span></h1>
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
          <h2>Active keys</h2>
          <table>
            <thead><tr><th>Username</th><th>Repos</th><th>Token</th><th></th></tr></thead>
            <tbody id="token-rows"></tbody>
          </table>
        </div>
      </div>
    </main>
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
  async createRepo(name, scaffoldFormidable, sourceURL) {
    const body = { name, scaffold_formidable: !!scaffoldFormidable };
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
