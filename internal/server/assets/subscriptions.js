// /admin/subscriptions page. Owns the Issue-key form and the token
// grid. Needs the repo list (for the repo picker + pre-select from
// ?repo=) and the credentials list (to decide whether the `mirror`
// ability is relevant — see KNOWN_ABILITIES below).

(function () {
  const { api, escapeHtml, initSidebar, guardSession, copyToClipboard } = window.Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];

  function repoNames() { return repoInfoCache.map(r => r.name); }

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

  function renderTokensGrid() {
    document.getElementById('count').textContent = tokensCache.length;
    const grid = document.getElementById('token-grid');
    const empty = document.getElementById('token-empty');
    grid.replaceChildren(...tokensCache.map(renderTokenCard));
    empty.classList.toggle('hidden', tokensCache.length !== 0);
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

  // Full page refresh: repos + tokens + creds. Called on boot and
  // after issue/revoke/edit so the ability picker, repo picker, and
  // token grid stay in sync.
  async function refreshAll() {
    try {
      const [repoData, tokenData, credData] = await Promise.all([
        api.listRepos().catch(() => ({ repos: [] })),
        api.listTokens().catch(() => ({ tokens: [] })),
        api.listCredentials().catch(() => ({ credentials: [] })),
      ]);
      repoInfoCache = repoData.repos || [];
      tokensCache = tokenData.tokens || [];
      credentialsCache = credData.credentials || [];
      renderTokensGrid();
      renderRepoPicker(document.getElementById('issue-repos'), []);
      syncIssueAbilities();

      // Pre-select repo from ?repo=<name> if the admin arrived here
      // via the "+ Issue key" button on a repo card.
      const preselect = new URLSearchParams(location.search).get('repo');
      if (preselect && repoNames().includes(preselect)) {
        renderRepoPicker(document.getElementById('issue-repos'), [preselect]);
        const u = document.querySelector('#issue-form [name="username"]');
        if (u) u.focus();
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
      try {
        const t = await api.issueToken(f.username.value, repos, abilities);
        msg.textContent = 'Issued: ' + t.token;
        f.reset();
        renderRepoPicker(document.getElementById('issue-repos'), []);
        refreshTokens();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refreshAll();
  })();
})();
