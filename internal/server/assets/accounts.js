// /admin/accounts — the account directory. Lists admins + regulars,
// lets an admin create new ones, flip roles, reset local passwords,
// and delete accounts. Session-guarded; a non-admin bounces on 401
// the same way every other admin page does.

(function () {
  const { api, escapeHtml, initSidebar, guardSession } = window.Admin;

  async function refresh() {
    try {
      const data = await api.listAccounts();
      const rows = data.accounts || [];
      document.getElementById('acct-count').textContent = rows.length;
      const tbody = document.getElementById('acct-rows');
      tbody.replaceChildren();
      for (const a of rows.sort(sortAccounts)) {
        tbody.appendChild(renderRow(a));
      }
    } catch (e) {
      console.error(e);
    }
  }

  // admins first, then alphabetical by (provider, identifier) — the
  // table is short enough that this is cheap and it keeps the
  // "who can log in" rows near the top where an admin looks first.
  function sortAccounts(a, b) {
    if (a.role !== b.role) return a.role === 'admin' ? -1 : 1;
    if (a.provider !== b.provider) return a.provider < b.provider ? -1 : 1;
    return a.identifier < b.identifier ? -1 : 1;
  }

  function renderRow(a) {
    const tr = document.createElement('tr');
    // Subscription cell: click-through to /admin/subscriptions filtered
    // by this account. Zero → muted dash (nothing to click).
    const subsCell = a.subscription_count > 0
      ? '<a class="badge sub-badge" href="/admin/subscriptions?user=' +
          encodeURIComponent(a.provider + ':' + a.identifier) + '">' +
          a.subscription_count + ' key' + (a.subscription_count === 1 ? '' : 's') +
        '</a>'
      : '<span class="muted">—</span>';
    tr.innerHTML =
      '<td><code>' + escapeHtml(a.provider) + '</code></td>' +
      '<td><code>' + escapeHtml(a.identifier) + '</code></td>' +
      '<td>' + escapeHtml(a.display_name || '') + '</td>' +
      '<td><span class="badge ' + (a.role === 'admin' ? 'formidable' : '') + '">' +
        escapeHtml(a.role) + '</span></td>' +
      '<td>' + (a.has_password ? 'yes' : '<span class="muted">dormant</span>') + '</td>' +
      '<td>' + subsCell + '</td>' +
      '<td class="muted">' + escapeHtml((a.created_at || '').slice(0, 10)) + '</td>' +
      '<td class="row-actions"></td>';

    const actions = tr.querySelector('.row-actions');

    const flipRoleBtn = document.createElement('button');
    flipRoleBtn.className = 'small secondary';
    flipRoleBtn.textContent = a.role === 'admin' ? 'Demote to regular' : 'Promote to admin';
    flipRoleBtn.addEventListener('click', async () => {
      const next = a.role === 'admin' ? 'regular' : 'admin';
      try {
        await api.patchAccount(a.provider, a.identifier, { role: next });
        refresh();
      } catch (e) { GG.dialog.alert('Role change failed', e.message); }
    });
    actions.appendChild(flipRoleBtn);

    if (a.provider === 'local') {
      const pwBtn = document.createElement('button');
      pwBtn.className = 'small secondary';
      pwBtn.textContent = a.has_password ? 'Reset password' : 'Set password';
      pwBtn.addEventListener('click', async () => {
        // GG.dialog.prompt isn't ported yet; native prompt() is
        // unavoidable here until we do. Acceptable because the value
        // never reaches the DOM — only the API call.
        const pw = prompt('New password for ' + a.identifier + ':');
        if (!pw) return;
        try {
          await api.patchAccount(a.provider, a.identifier, { password: pw });
          refresh();
        } catch (e) { GG.dialog.alert('Password update failed', e.message); }
      });
      actions.appendChild(pwBtn);
    }

    const delBtn = document.createElement('button');
    delBtn.className = 'small danger';
    delBtn.textContent = 'Delete';
    delBtn.addEventListener('click', async () => {
      const ok = await GG.dialog.confirm({
        title: 'Delete account',
        message: 'Delete ' + a.provider + ':' + a.identifier + '? This cannot be undone.',
        okText: 'Delete',
        dangerOk: true,
      });
      if (!ok) return;
      try {
        await api.deleteAccount(a.provider, a.identifier);
        refresh();
      } catch (e) { GG.dialog.alert('Delete failed', e.message); }
    });
    actions.appendChild(delBtn);

    return tr;
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('accounts', who.username);

    // Render the Provider + Role dropdowns with the shared .gsel chrome
    // so they match the Kind dropdown on the credentials page. The
    // hidden <input name=...> the component creates means the form's
    // submit handler keeps reading f.provider.value / f.role.value.
    const providerHost = document.getElementById('provider-host');
    if (providerHost) {
      providerHost.innerHTML = GG.select.html({
        name: 'provider',
        value: 'local',
        options: [
          { value: 'local',     label: 'local' },
          { value: 'github',    label: 'github' },
          { value: 'entra',     label: 'entra' },
          { value: 'microsoft', label: 'microsoft' },
          { value: 'gateway',   label: 'gateway' },
        ],
      });
      GG.select.initAll(providerHost);
    }
    const roleHost = document.getElementById('role-host');
    if (roleHost) {
      roleHost.innerHTML = GG.select.html({
        name: 'role',
        value: 'regular',
        options: [
          { value: 'regular', label: 'regular' },
          { value: 'admin',   label: 'admin' },
        ],
      });
      GG.select.initAll(roleHost);
    }

    document.getElementById('acct-form').addEventListener('submit', async e => {
      e.preventDefault();
      const f = e.target;
      const msg = document.getElementById('acct-msg');
      msg.textContent = '';
      msg.className = 'muted';
      const body = {
        provider: f.provider.value,
        identifier: f.identifier.value.trim(),
        role: f.role.value,
      };
      const display = f.display_name.value.trim();
      if (display) body.display_name = display;
      const pw = f.password.value;
      if (pw && body.provider === 'local') body.password = pw;

      try {
        await api.createAccount(body);
        msg.textContent = 'Account created.';
        msg.className = 'success';
        f.reset();
        refresh();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refresh();
  })();
})();
