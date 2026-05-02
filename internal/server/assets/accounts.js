// /admin/accounts — the account directory. Lists admins + regulars,
// lets an admin create new ones, flip roles, reset local passwords,
// and delete accounts. Session-guarded; a non-admin bounces on 401
// the same way every other admin page does.

(function () {
  const { api, escapeHtml, initSidebar, guardSession } = window.Admin;

  let tagsCatalogueCache = [];
  let accountsCache = [];
  // GG.tag_filter.attachClientSide controller. Renders the chip
  // filter, owns ?tag= URL state, and narrows the visible row set.
  // Mounted once on boot — page-level refresh() just re-fetches
  // data and asks the controller to re-render.
  let tagFilterCtl = null;

  // Per-account expand state, keyed by "provider:identifier". Persisted
  // across refresh() calls so a tag-edit (which triggers a re-render)
  // doesn't snap every open detail row shut. Same pattern as the
  // subsOpenState / destOpenState maps on /admin/repositories.
  const acctOpenState = Object.create(null);

  async function refresh() {
    try {
      const [data, tagData] = await Promise.all([
        api.listAccounts(),
        api.listTags().catch(() => ({ tags: [] })),
      ]);
      tagsCatalogueCache = (tagData.tags || []).map(t => t.name);
      accountsCache = (data.accounts || []).slice().sort(sortAccounts);
      // refresh() prunes the URL filter against the new dataset and
      // re-renders both the chips and the visible row list (which
      // calls renderRow for each surviving account).
      if (tagFilterCtl) tagFilterCtl.refresh();
    } catch (e) {
      console.error(e);
    }
  }

  // Role ordering: admins first, then maintainers, then regulars,
  // alphabetical by (provider, identifier) within each tier. Keeps
  // the "who can log in" + "who can manage mirrors" rows near the
  // top where an admin looks first.
  const ROLE_RANK = { admin: 0, maintainer: 1, regular: 2 };

  // roleBadgeAttrs is shared with the sidebar identity strip via
  // Admin.roleBadgeAttrs — one source of truth for "what colour is
  // this role" so the table, sidebar, and any future role display
  // never drift apart. Returns the `data-role="..."` fragment;
  // CSS in admin.css resolves the palette off the attribute.
  const roleBadgeAttrs = Admin.roleBadgeAttrs;
  function sortAccounts(a, b) {
    const ra = ROLE_RANK[a.role] ?? 99;
    const rb = ROLE_RANK[b.role] ?? 99;
    if (ra !== rb) return ra - rb;
    if (a.provider !== b.provider) return a.provider < b.provider ? -1 : 1;
    return a.identifier < b.identifier ? -1 : 1;
  }

  function renderRow(a) {
    const tr = document.createElement('tr');
    // Identifier rendering: OAuth subs can be 40+ chars of opaque
    // base64url and wrap the row into an unreadable vertical stack.
    // Truncate in the cell and expose the full value via `title` so
    // admins can still copy-read it on hover.
    // data-label drives the mobile card-list rendering — see
    // .responsive-table in admin.css. One source of column names
    // for both the table <th> and the mobile pseudo-label.
    const accountKey = a.provider + ':' + a.identifier;
    const tagCount = (a.tags || []).length;
    tr.innerHTML =
      '<td data-label="Provider">' +
        '<button type="button" class="acct-row-toggle" aria-label="Show account detail" title="Show details">▶</button>' +
        '<code>' + escapeHtml(a.provider) + '</code>' +
      '</td>' +
      '<td data-label="Identifier"><code class="acct-identifier" title="' + escapeHtml(a.identifier) + '">' +
        escapeHtml(a.identifier) + '</code></td>' +
      '<td data-label="Display name">' + escapeHtml(a.display_name || '') + '</td>' +
      '<td data-label="Role"><span class="badge"' + roleBadgeAttrs(a.role) + '>' +
        escapeHtml(a.role) + '</span></td>' +
      '<td class="row-actions"></td>';

    // The detail row sits directly below the main row. It collapses
    // by default; the toggle button above flips display: '' / 'none'.
    // colspan covers every visible column. The keep-visible columns
    // are identity-shaped (provider / identifier / display name /
    // role) — everything that's metadata about the account moves
    // here so the table stays scannable.
    // The detail row is a GG.lazy host — its body is rendered from
    // the `account-detail` fragment on first open. The toggle button
    // outside the host drives the render via GG.lazy.refresh(),
    // hence trigger="manual" so the helper doesn't bind its own
    // click handler on the row itself.
    const detailTr = document.createElement('tr');
    detailTr.className = 'acct-detail-row';
    detailTr.style.display = 'none';
    detailTr.dataset.lazyTpl = 'account-detail';
    detailTr.dataset.lazyTrigger = 'manual';

    GG.lazy.bind(detailTr, {
      // getData shapes the JSON the fragment renders against.
      // Conditional class+label pairs (password_muted /
      // password_label, subs_link_hidden / subs_empty_hidden) are
      // the workaround for the helper's deliberate lack of
      // {{#if}} — see lazy.md §4.3.
      getData: () => ({
        password_muted: a.has_password ? '' : 'muted',
        password_label: a.has_password ? 'yes' : 'dormant',
        subs_url: a.subscription_count > 0
          ? '/admin/subscriptions?user=' + encodeURIComponent(a.provider + ':' + a.identifier)
          : '#',
        subs_label: a.subscription_count > 0
          ? a.subscription_count + ' key' + (a.subscription_count === 1 ? '' : 's')
          : '',
        subs_link_hidden:  a.subscription_count > 0 ? '' : 'hidden',
        subs_empty_hidden: a.subscription_count > 0 ? 'hidden' : '',
        created_at: (a.created_at || '').slice(0, 10),
        tag_count: tagCount,
        tag_count_hidden: tagCount ? '' : 'hidden',
      }),
      // onRendered re-mounts the tag picker against the freshly
      // rendered .acct-tags-host. Fires on first open AND on every
      // refresh after a tag change, so the picker always reflects
      // the current state.
      onRendered: host => {
        const tagHost = host.querySelector('.acct-tags-host');
        if (!tagHost) return;
        GG.tag_picker.mount(tagHost, {
          tags: a.tags || [],
          allTags: tagsCatalogueCache,
          onChange: async (next) => {
            const resp = await api.setAccountTags(a.provider, a.identifier, next);
            a.tags = resp.tags || [];
            try {
              const data = await api.listTags();
              tagsCatalogueCache = (data.tags || []).map(t => t.name);
            } catch { /* leave cache as-is */ }
            if (tagFilterCtl) tagFilterCtl.refresh();
          },
        });
      },
    });

    const toggleBtn = tr.querySelector('.acct-row-toggle');
    // First-render guard: refresh() runs the lazy template + onRendered
    // exactly once on first open. Subsequent opens just unhide the
    // already-rendered row, so the picker's local state survives
    // the close→reopen toggle.
    let lazyRendered = false;
    function applyOpen(open) {
      detailTr.style.display = open ? '' : 'none';
      toggleBtn.classList.toggle('open', open);
      toggleBtn.textContent = open ? '▼' : '▶';
      toggleBtn.setAttribute('aria-label', open ? 'Hide account detail' : 'Show account detail');
      acctOpenState[accountKey] = open;
      if (open && !lazyRendered) {
        GG.lazy.refresh(detailTr);
        lazyRendered = true;
      }
    }
    toggleBtn.addEventListener('click', () => applyOpen(!acctOpenState[accountKey]));
    if (acctOpenState[accountKey]) applyOpen(true);

    const actions = tr.querySelector('.row-actions');

    // Build the action list declaratively, then hand it to row_menu.
    // Order: safe edits (rename) → state change (set role, reset
    // password) → destructive (delete). Conditional items use
    // `hidden: true` so the list remains a flat literal instead of
    // sprouting branches.
    function setRole(target) {
      return async () => {
        try {
          await api.patchAccount(a.provider, a.identifier, { role: target });
          refresh();
        } catch (e) { GG.dialog.alert('Role change failed', e.message); }
      };
    }
    async function patchDisplayName() {
      const next = await GG.dialog.prompt({
        title: 'Rename ' + a.provider + ':' + a.identifier,
        message: 'Shown in the sidebar and subscription cards. Leave blank to clear.',
        defaultValue: a.display_name || '',
        placeholder: 'e.g. Peter van de Pas',
        okText: 'Save',
      });
      if (next === null) return;
      try {
        await api.patchAccount(a.provider, a.identifier, { display_name: next });
        refresh();
      } catch (e) { GG.dialog.alert('Rename failed', e.message); }
    }
    async function resetPassword() {
      const pw = await GG.dialog.prompt({
        title: 'New password for ' + a.identifier,
        message: 'This replaces the current password. The account stays local.',
        placeholder: 'New password',
        okText: 'Set password',
        password: true,
      });
      if (pw === null || pw === '') return;
      try {
        await api.patchAccount(a.provider, a.identifier, { password: pw });
        refresh();
      } catch (e) { GG.dialog.alert('Password update failed', e.message); }
    }
    async function deleteAccount() {
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
    }

    GG.row_menu.attach(actions, [
      { label: a.display_name ? 'Rename' : 'Set display name', onClick: patchDisplayName },
      // Three role targets, current one hidden. Lets the admin move
      // between any two roles in one click instead of cycling.
      { label: 'Make admin',      onClick: setRole('admin'),      hidden: a.role === 'admin' },
      { label: 'Make maintainer', onClick: setRole('maintainer'), hidden: a.role === 'maintainer' },
      { label: 'Make regular',    onClick: setRole('regular'),    hidden: a.role === 'regular' },
      { label: a.has_password ? 'Reset password' : 'Set password', onClick: resetPassword,
        hidden: a.provider !== 'local' },
      { label: 'Delete', onClick: deleteAccount, danger: true },
    ]);

    // Return both rows as a fragment so the caller's appendChild
    // adds the data row + the (initially hidden) detail row in
    // order. tbody renders one logical record as two physical
    // rows, which is the standard table-with-collapsible pattern.
    const frag = document.createDocumentFragment();
    frag.appendChild(tr);
    frag.appendChild(detailTr);
    return frag;
  }

  // mountCreateAccountPickers re-mounts the Provider / Role
  // GG.select chrome inside the create-account fragment after each
  // render. Picker hosts are page-specific (the fragment carries
  // them as empty `<span id="...-host">` placeholders), so the
  // imperative initialisation has to run post-render.
  function mountCreateAccountPickers(host) {
    const providerHost = host.querySelector('#provider-host');
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
    const roleHost = host.querySelector('#role-host');
    if (roleHost) {
      roleHost.innerHTML = GG.select.html({
        name: 'role',
        value: 'regular',
        options: [
          { value: 'regular',    label: 'regular' },
          { value: 'maintainer', label: 'maintainer' },
          { value: 'admin',      label: 'admin' },
        ],
      });
      GG.select.initAll(roleHost);
    }
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('accounts', who);

    // Mount the tag filter — controller renders the chips, owns the
    // ?tag= URL state, and narrows the visible row list.
    tagFilterCtl = GG.tag_filter.attachClientSide({
      filterRow: document.getElementById('tag-filter'),
      emptyHint: 'No tags in use on any account yet. Add one to a row and the chip will appear here.',
      rows:    () => accountsCache,
      rowTags: a => a.tags || [],
      renderRows: visible => {
        document.getElementById('acct-count').textContent = visible.length;
        const tbody = document.getElementById('acct-rows');
        tbody.replaceChildren();
        for (const a of visible) tbody.appendChild(renderRow(a));
      },
    });

    // Create-account form lives in a fragment rendered into the
    // create-account drawer. GG.drawer.bindForm handles the lazy
    // bind + submit + close + error-into-#acct-msg dance; this
    // page only declares the picker mounts (onRendered), the API
    // call (submit), and the post-success refresh.
    GG.drawer.bindForm('create-account', {
      onRendered: mountCreateAccountPickers,
      submit: async data => {
        const body = {
          provider: data.provider,
          identifier: (data.identifier || '').trim(),
          role: data.role,
        };
        const display = (data.display_name || '').trim();
        if (display) body.display_name = display;
        if (data.password && body.provider === 'local') body.password = data.password;
        return api.createAccount(body);
      },
      onSuccess: refresh,
    });
    GG.drawer.attachAll();

    await refresh();
  })();
})();
