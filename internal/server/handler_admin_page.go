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
  input { background: #0d1117; border: 1px solid #30363d; color: #c9d1d9;
          padding: 0.5rem 0.75rem; border-radius: 6px; font-size: 0.9rem; min-width: 220px; }
  input:focus { outline: none; border-color: #58a6ff; }
  .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px;
          padding: 1.5rem; margin-bottom: 1rem; }
  .row { display: flex; gap: 0.5rem; align-items: center; }
  .muted { color: #8b949e; font-size: 0.85rem; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid #21262d; }
  th { color: #8b949e; font-weight: 500; font-size: 0.85rem; text-transform: uppercase; }
  code { background: #0d1117; padding: 2px 6px; border-radius: 4px; font-size: 0.85rem;
         word-break: break-all; }
  .error { color: #f85149; font-size: 0.9rem; margin-top: 0.5rem; }
  .success { color: #3fb950; font-size: 0.9rem; margin-top: 0.5rem; }
  .hidden { display: none; }
  form { display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: center; }
  .login-wrap { max-width: 380px; margin: 4rem auto; }
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
</div>

<div id="app" class="hidden">
  <header>
    <h1>GiGot Admin</h1>
    <div class="row">
      <span class="muted">signed in as <strong id="me-name"></strong></span>
      <button class="secondary" id="logout">Sign out</button>
    </div>
  </header>

  <div class="card">
    <h2>Issue subscription key</h2>
    <form id="issue-form">
      <input name="username" placeholder="Client username" required>
      <button type="submit">Issue key</button>
    </form>
    <div id="issue-msg" class="success"></div>
  </div>

  <div class="card">
    <h2>Active subscription keys (<span id="count">0</span>)</h2>
    <table>
      <thead><tr><th>Username</th><th>Token</th><th></th></tr></thead>
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
  async listTokens() {
    const r = await fetch('/api/admin/tokens', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('list failed');
    return r.json();
  },
  async issueToken(username) {
    const r = await fetch('/api/admin/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ username }),
    });
    if (!r.ok) throw new Error((await r.json()).error || 'issue failed');
    return r.json();
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

function show(who) {
  const loggedIn = who && who.username;
  loginView.classList.toggle('hidden', !!loggedIn);
  appView.classList.toggle('hidden', !loggedIn);
  if (loggedIn) {
    document.getElementById('me-name').textContent = who.username;
    refreshTokens();
  }
}

async function refreshTokens() {
  try {
    const data = await api.listTokens();
    document.getElementById('count').textContent = data.count;
    const rows = data.tokens.map(t => {
      const tr = document.createElement('tr');
      tr.innerHTML =
        '<td>' + escapeHtml(t.username) + '</td>' +
        '<td><code>' + escapeHtml(t.token) + '</code></td>' +
        '<td><button class="danger" data-token="' + escapeHtml(t.token) + '">Revoke</button></td>';
      return tr;
    });
    const tbody = document.getElementById('token-rows');
    tbody.replaceChildren(...rows);
    tbody.querySelectorAll('button.danger').forEach(b => {
      b.addEventListener('click', async () => {
        if (!confirm('Revoke this key?')) return;
        await api.revokeToken(b.dataset.token);
        refreshTokens();
      });
    });
  } catch (e) {
    console.error(e);
  }
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
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

document.getElementById('issue-form').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const msg = document.getElementById('issue-msg');
  msg.textContent = '';
  try {
    const t = await api.issueToken(f.username.value);
    msg.textContent = 'Issued: ' + t.token;
    f.reset();
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
