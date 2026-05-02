// /admin/credentials page. Owns the Add-credential form and the
// credential grid. Uses Admin.api for the credential endpoints and
// Admin.initSidebar for the shared left-rail chrome.

(function () {
  const { api, escapeHtml, initSidebar, guardSession } = window.Admin;

  // GG.text_filter.attachClientSide controller. Free-text search
  // across name + kind + notes — credentials aren't tagged so a
  // chip filter doesn't fit; substring search does.
  let filterCtl = null;
  let credentialsCache = [];

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

  // renderRow builds one credential <tr> with a notes button (opens
  // a dialog when clicked) instead of inline notes text. Empty
  // notes hide the button via the .hidden class instead of leaving
  // a trailing dash.
  function renderRow(c) {
    const tr = document.createElement('tr');
    const expBucket = classifyExpires(c.expires);
    const expClass = expBucket === 'expired'
      ? 'cred-expired'
      : expBucket === 'expiring'
        ? 'cred-expiring'
        : expBucket === 'none' ? 'muted' : '';
    const expTitle = expBucket === 'expired'
      ? ' title="Already expired. Rotate this credential."'
      : expBucket === 'expiring'
        ? ' title="Expires within 7 days. Rotate soon."'
        : '';
    const hasNotes = !!(c.notes && c.notes.trim());
    // data-label drives the mobile card-list rendering — see the
    // .responsive-table block at the bottom of admin.css. Labels
    // mirror the column <th> text so the desktop and mobile views
    // are conceptually identical, just folded differently.
    tr.innerHTML =
      '<td data-label="Name"><code>' + escapeHtml(c.name) + '</code></td>' +
      '<td data-label="Kind">' + escapeHtml(c.kind) + '</td>' +
      '<td data-label="Expires" class="' + expClass + '"' + expTitle + '>' + escapeHtml(formatExpires(c.expires)) + '</td>' +
      '<td data-label="Notes">' +
        (hasNotes
          ? '<button type="button" class="small secondary cred-notes-btn" title="Show notes">Note</button>'
          : '<span class="muted">—</span>') +
      '</td>' +
      '<td data-label="Last used" class="muted">' + escapeHtml(formatWhen(c.last_used)) + '</td>' +
      '<td class="row-actions"></td>';

    if (hasNotes) {
      tr.querySelector('.cred-notes-btn').addEventListener('click', () => {
        GG.dialog.alert('Notes for ' + c.name, c.notes);
      });
    }

    async function deleteCredential() {
      const ok = await GG.dialog.confirm({
        title: 'Delete credential',
        message: 'Delete credential "' + c.name + '"? The sealed secret is destroyed. You can\'t recover it from the server.',
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
  }

  async function refresh() {
    const data = await api.listCredentials();
    credentialsCache = data.credentials || [];
    if (filterCtl) filterCtl.refresh();
  }

  // mountCreateCredentialChrome — re-runs the imperative GG.select +
  // GG.datepicker initialisation against the freshly rendered
  // create-credential fragment. Kind dropdown picker and the
  // calendar popup on the Expires field both live outside the
  // simple `<input>` set the helper handles by default.
  function mountCreateCredentialChrome(host) {
    const kindHost = host.querySelector('#kind-host');
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
    // The data-gdp attribute on the Expires input is the cue
    // GG.datepicker.initAll picks up; scope the call to this
    // host so other date inputs on the page (none today, but
    // future-proof) stay untouched.
    if (window.GG && GG.datepicker) GG.datepicker.initAll(host);
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('credentials', who);

    // Free-text search across name + kind + notes. Credentials
    // aren't tagged so the chip filter doesn't apply; with many
    // similar PATs ("github-personal", "github-work", "azure-pat-…")
    // the admin needs substring search to find one fast.
    filterCtl = GG.text_filter.attachClientSide({
      filterRow:   document.getElementById('cred-filter'),
      placeholder: 'Search by name, kind, or notes…',
      emptyHint:   'No credentials in the vault yet.',
      rows:        () => credentialsCache,
      rowText:     c => [c.name, c.kind, c.notes].filter(Boolean).join(' '),
      renderRows:  visible => {
        document.getElementById('cred-count').textContent = visible.length;
        const tbody = document.getElementById('cred-rows');
        tbody.replaceChildren(...visible.map(renderRow));
      },
    });

    // Add-credential form lives in a fragment rendered into the
    // create-credential drawer. GG.drawer.bindForm handles the
    // lazy bind + submit + close + error-into-#cred-msg dance;
    // this page only declares the picker/datepicker mounts
    // (onRendered), the API call (submit), and the post-success
    // refresh.
    GG.drawer.bindForm('create-credential', {
      onRendered: mountCreateCredentialChrome,
      submit: async data => {
        const body = {
          name: (data.name || '').trim(),
          kind: data.kind,
          secret: data.secret,
          notes: (data.notes || '').trim(),
        };
        // data-gdp yields "YYYY-MM-DD" when filled, "" when cleared.
        // Normalise to a UTC midnight timestamp so server-side
        // *time.Time is unambiguous.
        const expRaw = (data.expires || '').trim();
        if (expRaw) body.expires = new Date(expRaw + 'T00:00:00Z').toISOString();
        return api.createCredential(body);
      },
      onSuccess: refresh,
    });
    GG.drawer.attachAll();

    await refresh();
  })();
})();
