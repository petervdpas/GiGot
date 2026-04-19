const api = {
  async session() {
    const r = await fetch('/api/admin/session', { credentials: 'same-origin' });
    return r.ok ? r.json() : null;
  },
  async logout() {
    await fetch('/admin/logout', { method: 'POST', credentials: 'same-origin' });
  },
  async list() {
    const r = await fetch('/api/admin/credentials', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('list failed');
    return r.json();
  },
  async create(body) {
    const r = await fetch('/api/admin/credentials', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.json()).error || 'create failed');
    return r.json();
  },
  async remove(name) {
    const r = await fetch('/api/admin/credentials/' + encodeURIComponent(name), {
      method: 'DELETE',
      credentials: 'same-origin',
    });
    if (!r.ok) throw new Error('delete failed');
  },
};

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function formatWhen(ts) {
  if (!ts) return 'never';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

async function refresh() {
  const data = await api.list();
  document.getElementById('cred-count').textContent = data.count;
  const tbody = document.getElementById('cred-rows');
  tbody.replaceChildren(...data.credentials.map(c => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      '<td><code>' + escapeHtml(c.name) + '</code></td>' +
      '<td>' + escapeHtml(c.kind) + '</td>' +
      '<td>' + escapeHtml(c.notes || '') + '</td>' +
      '<td class="muted">' + escapeHtml(formatWhen(c.last_used)) + '</td>' +
      '<td><button class="small danger">Delete</button></td>';
    tr.querySelector('button').addEventListener('click', async () => {
      if (!confirm('Delete credential "' + c.name + '"?')) return;
      try {
        await api.remove(c.name);
        await refresh();
      } catch (e) {
        alert(e.message);
      }
    });
    return tr;
  }));
}

document.getElementById('cred-form').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const msg = document.getElementById('cred-msg');
  msg.textContent = '';
  msg.className = 'muted';
  try {
    await api.create({
      name: f.name.value.trim(),
      kind: f.kind.value,
      secret: f.secret.value,
      notes: f.notes.value.trim(),
    });
    f.reset();
    await refresh();
  } catch (ex) {
    msg.textContent = ex.message;
    msg.className = 'error';
  }
});

document.getElementById('logout').addEventListener('click', async () => {
  await api.logout();
  location.href = '/admin';
});

(async () => {
  const who = await api.session();
  if (!who) {
    location.href = '/admin';
    return;
  }
  document.getElementById('me-name').textContent = who.username;
  await refresh();
})();
