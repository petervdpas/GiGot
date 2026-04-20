// /admin/subscriptions page. Owns the Issue-key form and the token
// grid. Needs the repo list (for the repo picker + pre-select from
// ?repo=) and the credentials list (to decide whether the `mirror`
// ability is relevant — see KNOWN_ABILITIES below).

(function () {
  const { api, escapeHtml, initSidebar, guardSession, copyToClipboard } = window.Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let accountsCache = [];

  function repoNames() { return repoInfoCache.map(r => r.name); }

  // accountOption turns a row from /api/admin/accounts into the
  // { value, label } pair the GG.select picker wants. Value is the
  // scoped "provider:identifier" string the server's token binding
  // expects; label is human — display name, identifier, provider in
  // parens. Keep regulars before admins (admins rarely hold subs but
  // the server allows it, so we surface them at the bottom).
  function accountOption(a) {
    const pretty = a.display_name ? a.display_name + ' — ' + a.identifier : a.identifier;
    return {
      value: a.provider + ':' + a.identifier,
      label: pretty + ' (' + a.provider + (a.role === 'admin' ? ' / admin' : '') + ')',
    };
  }
  function accountOptionsSorted() {
    const regulars = accountsCache.filter(a => a.role === 'regular').map(accountOption);
    const admins   = accountsCache.filter(a => a.role === 'admin').map(accountOption);
    return regulars.concat(admins);
  }

  // ──────────────────────────────────────────────────────────────── pickers

  function renderRepoPicker(container, selected) {
    const sel = new Set(selected || []);
    container.innerHTML = '';
    const names = repoNames();
    if (names.length === 0) {
      container.innerHTML = '<span class="muted">No repos exist yet — create one on the Repositories page.</span>';
      return;
    }
    for (const name of names) {
      const row = document.createElement('div');
      row.className = 'switch-row';
      row.innerHTML = GG.toggle_switch.html({
        value: name,
        checked: sel.has(name),
        ariaLabel: 'Allow access to ' + name,
      }) + '<span class="control-label">' + escapeHtml(name) + '</span>';
      container.appendChild(row);
    }
  }

  function selectedReposFromPicker(container) {
    return Array.from(container.querySelectorAll('.switch input[type=checkbox]:checked'))
      .map(cb => cb.value);
  }

  // KNOWN_ABILITIES mirrors internal/auth.KnownAbilities(). Each
  // entry has a `relevant` predicate: an ability is only offered in
  // the UI when its precondition holds, otherwise granting it would
  // be inert (e.g. `mirror` with no credentials in the vault means
  // the holder has nothing to reference when creating a destination).
  // The server-side allowlist still rejects anything not named here
  // via 400, so this list must stay in lockstep with Go's list.
  const KNOWN_ABILITIES = [
    {
      name: 'mirror',
      hint: 'self-manage remote destinations (remote-sync.md §2.6)',
      relevant: () => credentialsCache.length > 0,
      inertHint: 'add a credential first — a mirror destination needs one to reference',
    },
  ];

  function relevantAbilities() {
    return KNOWN_ABILITIES.filter(a => a.relevant());
  }

  function renderAbilityPicker(container, selected) {
    container.replaceChildren();
    const set = new Set(selected);
    const relevant = relevantAbilities();
    const names = new Set(relevant.map(a => a.name));
    for (const a of relevant) {
      container.append(makeAbilityChip(a, set.has(a.name), false));
    }
    // Held but no longer relevant — render as muted so the admin can revoke.
    for (const name of set) {
      if (!names.has(name)) {
        container.append(makeAbilityChip(
          { name, hint: 'granted but not currently actionable', inertHint: '' },
          true, true,
        ));
      }
    }
  }

  function makeAbilityChip(ability, checked, stale) {
    const row = document.createElement('div');
    row.className = 'switch-row' + (stale ? ' stale' : '');
    row.innerHTML = GG.toggle_switch.html({
      name: 'ability',
      value: ability.name,
      checked,
      ariaLabel: 'Grant ability ' + ability.name,
    }) +
      '<span class="control-label">' + escapeHtml(ability.name) + '</span>' +
      '<span class="muted ability-hint">' + escapeHtml(ability.hint) + '</span>';
    return row;
  }

  function selectedAbilitiesFromPicker(container) {
    return Array.from(container.querySelectorAll('.switch input[name="ability"]:checked'))
      .map(cb => cb.value);
  }

  function syncIssueAbilities() {
    const wrap = document.getElementById('issue-abilities-wrap');
    const picker = document.getElementById('issue-abilities');
    const any = relevantAbilities().length > 0;
    wrap.classList.toggle('hidden', !any);
    if (any) renderAbilityPicker(picker, []);
  }

  // ─────────────────────────────────────────────────────────── token card

  function renderTokenCard(t) {
    const card = document.createElement('div');
    card.className = 'info-card';
    card.dataset.token = t.token;

    const repos = (t.repos && t.repos.length)
      ? t.repos.map(r => '<span class="repo-chip">' + escapeHtml(r) + '</span>').join('')
      : '<span class="repo-chip none">no repos</span>';

    const abilities = (t.abilities && t.abilities.length)
      ? t.abilities.map(a => '<span class="ability-badge">' + escapeHtml(a) + '</span>').join('')
      : '';

    // Legacy badge: the token predates the accounts model and has no
    // account row to bind to. The Bind action on the card creates a
    // role=regular account for this username so the token is no longer
    // dangling. See accounts.md §6.
    const legacyBadge = t.has_account ? '' :
      '<span class="badge" title="This key was issued before the accounts model shipped. Click Bind to create a regular account for it.">legacy — no account</span>';

    card.innerHTML =
      '<div class="ic-header">' +
        '<div class="ic-title">' + escapeHtml(t.username) + '</div>' +
        '<div class="ic-chips">' + legacyBadge + '<span class="badge formidable">' + (t.repos ? t.repos.length : 0) + ' ' +
          ((t.repos && t.repos.length === 1) ? 'repo' : 'repos') + '</span></div>' +
      '</div>' +
      '<div class="ic-chips cell-repos">' + repos + '</div>' +
      (abilities ? '<div class="ic-chips cell-abilities">' + abilities + '</div>' : '') +
      '<div class="token-field">' +
        '<code class="token-value">' + escapeHtml(t.token) + '</code>' +
        '<button type="button" class="copy-btn">Copy</button>' +
      '</div>' +
      '<div class="ic-actions cell-actions">' +
        (t.has_account ? '' : '<button class="small bind-btn">Bind to account</button>') +
        '<button class="small secondary edit-btn">Edit access</button>' +
        '<button class="small danger revoke-btn">Revoke</button>' +
      '</div>';

    card.querySelector('.copy-btn').addEventListener('click', e => copyToClipboard(t.token, e.currentTarget));
    card.querySelector('.revoke-btn').addEventListener('click', async () => {
      const ok = await GG.dialog.confirm({
        title: 'Revoke subscription key',
        message: 'Revoke this key? Holder loses access immediately and the key can\'t be restored.',
        okText: 'Revoke',
        dangerOk: true,
      });
      if (!ok) return;
      await api.revokeToken(t.token);
      refreshTokens();
    });
    card.querySelector('.edit-btn').addEventListener('click', () => enterEditMode(card, t));
    const bind = card.querySelector('.bind-btn');
    if (bind) {
      bind.addEventListener('click', async () => {
        try {
          await api.bindToken(t.token);
          refreshTokens();
        } catch (e) { GG.dialog.alert('Bind failed', e.message); }
      });
    }
    return card;
  }

  function enterEditMode(card, t) {
    const cellRepos = card.querySelector('.cell-repos');
    const cellActions = card.querySelector('.cell-actions');

    const picker = document.createElement('div');
    picker.className = 'repo-picker';
    renderRepoPicker(picker, t.repos || []);
    cellRepos.replaceChildren(picker);

    const abilityEditor = document.createElement('div');
    abilityEditor.className = 'ability-picker';
    renderAbilityPicker(abilityEditor, t.abilities || []);
    cellRepos.after(abilityEditor);

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
      const abilities = selectedAbilitiesFromPicker(abilityEditor);
      save.disabled = true;
      try {
        await api.updateToken(t.token, { repos, abilities });
        refreshTokens();
      } catch (e) {
        save.disabled = false;
        status.textContent = e.message;
        status.className = 'error';
      }
    });

    cancel.addEventListener('click', () => {
      const fresh = renderTokenCard(t);
      card.replaceWith(fresh);
    });
  }

  // userFilter pulls ?user=<scoped> off the URL. When set, the token
  // grid narrows to rows whose stored username matches (either the
  // scoped form or, for back-compat, the bare local shorthand).
  function userFilter() {
    return (new URLSearchParams(location.search).get('user') || '').toLowerCase();
  }
  function tokenMatchesUser(t, scoped) {
    if (!scoped) return true;
    const legacyLocal = scoped.startsWith('local:') ? scoped.slice('local:'.length) : null;
    return t.username === scoped || (legacyLocal && t.username === legacyLocal);
  }

  function renderTokensGrid() {
    const scoped = userFilter();
    const visible = scoped ? tokensCache.filter(t => tokenMatchesUser(t, scoped)) : tokensCache;
    document.getElementById('count').textContent = visible.length;
    const grid = document.getElementById('token-grid');
    const empty = document.getElementById('token-empty');
    grid.replaceChildren(...visible.map(renderTokenCard));
    empty.classList.toggle('hidden', visible.length !== 0);
  }

  async function refreshTokens() {
    try {
      const data = await api.listTokens();
      tokensCache = data.tokens || [];
      renderTokensGrid();
    } catch (e) {
      console.error(e);
    }
  }

  // Render / re-render the Account picker (GG.select) with current
  // accountsCache. Preserves whatever scoped value was selected.
  function renderAccountPicker(selectedValue) {
    const host = document.getElementById('issue-account-host');
    if (!host) return;
    const opts = accountOptionsSorted();
    const value = selectedValue && opts.some(o => o.value === selectedValue)
      ? selectedValue
      : (opts[0] ? opts[0].value : '');
    host.innerHTML = GG.select.html({
      name: 'username',
      value,
      options: opts.length
        ? opts
        : [{ value: '', label: 'No accounts yet — register or create one first', disabled: true }],
    });
    GG.select.initAll(host);
  }

  // Full page refresh: repos + tokens + creds + accounts. Called on
  // boot and after issue/revoke/edit so every picker stays in sync.
  async function refreshAll() {
    try {
      const [repoData, tokenData, credData, acctData] = await Promise.all([
        api.listRepos().catch(() => ({ repos: [] })),
        api.listTokens().catch(() => ({ tokens: [] })),
        api.listCredentials().catch(() => ({ credentials: [] })),
        api.listAccounts().catch(() => ({ accounts: [] })),
      ]);
      repoInfoCache = repoData.repos || [];
      tokensCache = tokenData.tokens || [];
      credentialsCache = credData.credentials || [];
      accountsCache = acctData.accounts || [];
      renderTokensGrid();
      renderRepoPicker(document.getElementById('issue-repos'), []);
      syncIssueAbilities();

      // ?user= pre-selects the account picker. ?repo= pre-ticks the
      // repo picker. Both live behind the same "I arrived via a link
      // on another page" pattern, so they coexist cleanly.
      const params = new URLSearchParams(location.search);
      const preUser = params.get('user');
      renderAccountPicker(preUser || '');
      const preRepo = params.get('repo');
      if (preRepo && repoNames().includes(preRepo)) {
        renderRepoPicker(document.getElementById('issue-repos'), [preRepo]);
      }
    } catch (e) {
      console.error(e);
    }
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('subscriptions', who.username);

    document.getElementById('issue-form').addEventListener('submit', async e => {
      e.preventDefault();
      const f = e.target;
      const msg = document.getElementById('issue-msg');
      msg.textContent = '';
      const repos = selectedReposFromPicker(document.getElementById('issue-repos'));
      const abilities = selectedAbilitiesFromPicker(document.getElementById('issue-abilities'));
      // Read the picker's hidden input — GG.select projects its value
      // onto an <input name="username"> inside the host span.
      const usernameEl = document.querySelector('#issue-account-host input[name="username"]');
      const scoped = usernameEl ? usernameEl.value : '';
      if (!scoped) {
        msg.textContent = 'Pick an account to issue the key to.';
        msg.className = 'error';
        return;
      }
      try {
        const t = await api.issueToken(scoped, repos, abilities);
        msg.textContent = 'Issued: ' + t.token;
        // Reset the repo + ability pickers but leave the account
        // picker alone — issuing several keys for the same holder is
        // a common pattern and retyping would be annoying.
        renderRepoPicker(document.getElementById('issue-repos'), []);
        refreshAll();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refreshAll();
  })();
})();
