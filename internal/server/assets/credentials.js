// /admin/credentials page. Owns the Add-credential form and the
// credential grid. Uses Admin.api for the credential endpoints and
// Admin.initSidebar for the shared left-rail chrome.

(function () {
  const { api, escapeHtml, initSidebar, guardSession } = window.Admin;

  function formatWhen(ts) {
    if (!ts) return 'never';
    try { return new Date(ts).toLocaleString(); } catch { return ts; }
  }

  // classifyExpires turns an ISO timestamp into a bucket the table
  // uses to colour the cell. "expired" beats "expiring" so an admin
  // who's already past the date sees red, not amber. The 7-day
  // window matches docs/design/credential-vault.md §3.
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
    try { return new Date(ts).toLocaleDateString(); } catch { return ts; }
  }

  async function refresh() {
    const data = await api.listCredentials();
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
        '<td class="row-actions"></td>';

      async function deleteCredential() {
        const ok = await GG.dialog.confirm({
          title: 'Delete credential',
          message: 'Delete credential "' + c.name + '"? The sealed secret is destroyed — you can\'t recover it from the server.',
          okText: 'Delete',
          dangerOk: true,
        });
        if (!ok) return;
        try {
          await api.deleteCredential(c.name);
          await refresh();
        } catch (e) {
          // 409 means one or more repo destinations still reference
          // this credential — surface the repo list so the operator
          // knows where to go clear the references first.
          if (e.refRepos && e.refRepos.length) {
            await GG.dialog.alert(
              'Credential still in use',
              e.message + '\n\nRepos still referencing this credential:\n  • ' +
              e.refRepos.join('\n  • ') +
              '\n\nRetarget or remove those destinations, then try again.'
            );
          } else {
            await GG.dialog.alert('Delete failed', e.message);
          }
        }
      }

      GG.row_menu.attach(tr.querySelector('.row-actions'), [
        { label: 'Delete', onClick: deleteCredential, danger: true },
      ]);
      return tr;
    }));
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('credentials', who);

    // Render the Kind dropdown (.gsel) into its placeholder so it
    // matches the rest of the form chrome.
    const kindHost = document.getElementById('kind-host');
    if (kindHost) {
      kindHost.innerHTML = GG.select.html({
        name: 'kind',
        value: 'pat',
        options: [
          { value: 'pat',       label: 'Personal access token' },
          { value: 'user_pass', label: 'Username + password' },
          { value: 'ssh',       label: 'SSH key' },
          { value: 'other',     label: 'Other' },
        ],
      });
      GG.select.initAll(kindHost);
    }
    // Bind the custom calendar popup to the expires field — the
    // native <input type="date"> popup ignores our CSS tokens on
    // Firefox/Linux.
    GG.datepicker.initAll(document.getElementById('cred-form'));

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
        // <input type="text" data-gdp> yields "YYYY-MM-DD" when
        // filled, "" when cleared. Normalise to a UTC midnight
        // timestamp so server-side *time.Time is unambiguous.
        const expRaw = f.expires.value.trim();
        if (expRaw) body.expires = new Date(expRaw + 'T00:00:00Z').toISOString();
        await api.createCredential(body);
        f.reset();
        await refresh();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refresh();
  })();
})();
