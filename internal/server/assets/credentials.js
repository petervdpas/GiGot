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

// classifyExpires turns an ISO timestamp into a bucket the table uses to
// colour the cell. "expired" beats "expiring" so an admin who's already
// past the date sees red, not amber. The 7-day window matches
// docs/design/credential-vault.md §3.
function classifyExpires(ts) {
  if (!ts) return 'none';
  const exp = new Date(ts).getTime();
  if (Number.isNaN(exp)) return 'none';
  const now = Date.now();
  if (exp <= now) return 'expired';
  if (exp - now <= 7 * 24 * 60 * 60 * 1000) return 'expiring';
  return 'ok';
}

function formatExpires(ts) {
  if (!ts) return '—';
  try {
    return new Date(ts).toLocaleDateString();
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
    const expBucket = classifyExpires(c.expires);
    const expClass = expBucket === 'expired'
      ? 'cred-expired'
      : expBucket === 'expiring'
        ? 'cred-expiring'
        : expBucket === 'none' ? 'muted' : '';
    const expTitle = expBucket === 'expired'
      ? ' title="Already expired — rotate this credential."'
      : expBucket === 'expiring'
        ? ' title="Expires within 7 days — rotate soon."'
        : '';
    tr.innerHTML =
      '<td><code>' + escapeHtml(c.name) + '</code></td>' +
      '<td>' + escapeHtml(c.kind) + '</td>' +
      '<td class="' + expClass + '"' + expTitle + '>' + escapeHtml(formatExpires(c.expires)) + '</td>' +
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
    const body = {
      name: f.name.value.trim(),
      kind: f.kind.value,
      secret: f.secret.value,
      notes: f.notes.value.trim(),
    };
    // <input type="date"> yields "YYYY-MM-DD" when filled, "" when not.
    // Normalise to a UTC midnight timestamp so server-side *time.Time
    // gets an unambiguous value.
    const expRaw = f.expires.value.trim();
    if (expRaw) body.expires = new Date(expRaw + 'T00:00:00Z').toISOString();
    await api.create(body);
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
