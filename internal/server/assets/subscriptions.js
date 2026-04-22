// /admin/subscriptions page. Owns the Issue-key form and the token
// grid. Needs the repo list (for the repo picker + pre-select from
// ?repo=) and the credentials list (to decide whether the `mirror`
// ability is relevant — see KNOWN_ABILITIES below).

(function () {
  const Admin = window.Admin;
  const { api, escapeHtml, initSidebar, guardSession, copyToClipboard } = Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let accountsCache = [];

  function repoNames() { return repoInfoCache.map(r => r.name); }

  // Regulars first, admins last — admins can hold subscription
  // keys but they're the rarer case, so we surface them at the
  // bottom of the dropdown. accountOption comes from Admin so the
  // label format stays consistent with the sidebar / user strip.
  function accountOptionsSorted() {
    const regulars = accountsCache.filter(a => a.role === 'regular').map(Admin.accountOption);
    const admins   = accountsCache.filter(a => a.role === 'admin').map(Admin.accountOption);
    return regulars.concat(admins);
  }

  // resolveAccountForToken is a thin binding over Admin.resolveAccount
  // that closes over the page's accountsCache. Kept as a local alias
  // so the call sites stay short; parsing rules live in one place.
  function resolveAccountForToken(username) {
    return Admin.resolveAccount(username, accountsCache);
  }

  // ──────────────────────────────────────────────────────────────── pickers

  // renderRepoSelect renders a GG.select picker for a single repo into
  // `host`, replacing any existing markup. Empty repos list → a
  // disabled placeholder so the dropdown still looks intentional.
  // Subscription keys bind to exactly one repo; the multi-toggle
  // picker that used to live here is gone with the data model.
  // `onChange(repoName)` fires whenever the user picks a new value —
  // used by the issue and edit flows to re-sync the abilities row
  // (mirror is only relevant when the selected repo has a
  // destination).
  function renderRepoSelect(host, value, onChange) {
    const names = repoNames();
    if (names.length === 0) {
      host.innerHTML = GG.select.html({
        name: 'repo',
        value: '',
        options: [{ value: '', label: 'No repos exist yet — create one on the Repositories page.', disabled: true }],
      });
      GG.select.initAll(host);
      return;
    }
    host.innerHTML = GG.select.html({
      name: 'repo',
      value: value || '',
      placeholder: 'Pick a repo…',
      options: names.map(n => ({ value: n, label: n })),
    });
    GG.select.initAll(host);
    if (onChange) {
      const sel = host.querySelector('.gsel');
      if (sel) GG.select.init(sel, onChange);
    }
  }

  function selectedRepoFromHost(host) {
    const hidden = host.querySelector('input[name="repo"]');
    return hidden ? hidden.value : '';
  }

  // repoHasMirror returns true when the named repo has at least
  // one mirror destination configured. Reads from repoInfoCache —
  // RepoInfo.DestinationCount is populated by the /api/repos
  // listing, so no extra fetch is needed.
  function repoHasMirror(repoName) {
    if (!repoName) return false;
    const r = repoInfoCache.find(x => x.name === repoName);
    return !!(r && r.destination_count > 0);
  }

  // KNOWN_ABILITIES mirrors internal/auth.KnownAbilities(). Each
  // entry has a `relevant(repoName)` predicate: an ability is only
  // offered in the UI when BOTH the global precondition (e.g. at
  // least one credential exists) AND the per-repo precondition
  // (e.g. the repo has a mirror destination to manage) hold.
  // Granting an ability that can't be acted on would mislead the
  // admin. The server-side allowlist still rejects anything not
  // named here via 400, so this list must stay in lockstep with
  // Go's list.
  const KNOWN_ABILITIES = [
    {
      name: 'mirror',
      hint: 'self-manage remote destinations',
      relevant: (repoName) =>
        credentialsCache.length > 0 && repoHasMirror(repoName),
    },
  ];

  function relevantAbilities(repoName) {
    return KNOWN_ABILITIES.filter(a => a.relevant(repoName));
  }

  function renderAbilityPicker(container, selected, repoName) {
    container.replaceChildren();
    const set = new Set(selected);
    const relevant = relevantAbilities(repoName);
    const names = new Set(relevant.map(a => a.name));
    for (const a of relevant) {
      container.append(makeAbilityChip(a, set.has(a.name), false));
    }
    // Held but no longer relevant for THIS repo — render muted so
    // the admin can revoke it on the current key without rebinding.
    for (const name of set) {
      if (!names.has(name)) {
        container.append(makeAbilityChip(
          { name, hint: 'granted but not applicable to this repo' },
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
    const repoHost = document.getElementById('issue-repo-host');
    const repoName = repoHost ? selectedRepoFromHost(repoHost) : '';
    const any = relevantAbilities(repoName).length > 0;
    wrap.classList.toggle('hidden', !any);
    if (any) renderAbilityPicker(picker, [], repoName);
  }

  // ─────────────────────────────────────────────────────────── token card

  // Wraps Admin.renderTokenCard with the admin-specific extras:
  // display-name-resolving title, legacy-account badge, and the
  // bind/edit/revoke actions. The card body (repos, abilities,
  // token + copy) is shared across /admin/subscriptions and /user,
  // so changes to that visual stay in one place.
  function renderTokenCard(t) {
    const resolved = resolveAccountForToken(t.username);
    const title = resolved ? Admin.accountLabel(resolved) : t.username;
    const subtitle = resolved
      ? '<code class="acct-identifier" title="' + escapeHtml(t.username) + '">' +
          escapeHtml(resolved.provider) + ':' + escapeHtml(resolved.identifier) + '</code>'
      : null;
    const leftChips = t.has_account ? null :
      '<span class="badge" title="This key was issued before the accounts model shipped. Click Bind to create a regular account for it.">legacy — no account</span>';

    const actions = [];
    if (!t.has_account) {
      actions.push({
        label: 'Bind to account',
        onClick: async () => {
          try {
            await api.bindToken(t.token);
            refreshTokens();
          } catch (e) { GG.dialog.alert('Bind failed', e.message); }
        },
      });
    }
    // No "Edit access" button — the abilities collapsible is the
    // edit gesture for what admins change day-to-day. Rebinding a
    // key to a different repo is rare and better modelled as
    // revoke + re-issue (clean audit trail, no stale abilities
    // carried over), so it's not a primary action on the card.
    actions.push({
      label: 'Revoke',
      className: 'danger',
      onClick: async () => {
        const ok = await GG.dialog.confirm({
          title: 'Revoke subscription key',
          message: 'Revoke this key? Holder loses access immediately and the key can\'t be restored.',
          okText: 'Revoke',
          dangerOk: true,
        });
        if (!ok) return;
        await api.revokeToken(t.token);
        refreshTokens();
      },
    });

    const card = Admin.renderTokenCard(t, { title, subtitle, leftChips, actions });
    installAbilitiesSection(card, t);
    return card;
  }

  // installAbilitiesSection drops the flat chip row that
  // Admin.renderTokenCard inserts and replaces it with a
  // collapsible section whose body contains editable toggles + a
  // right-aligned Save button. Toggles DON'T save on flip — they
  // just mark the section dirty. Save commits, then the section
  // snaps back to its pristine state WITHOUT re-rendering the
  // card grid (that was causing collapsibles to snap shut and
  // cards to reorder on every flip).
  function installAbilitiesSection(card, t) {
    const flatChips = card.querySelector(':scope > .ic-chips.cell-abilities');
    if (flatChips) flatChips.remove();
    const tokenField = card.querySelector('.token-field');
    if (!tokenField) return null;

    const details = document.createElement('details');
    details.className = 'ic-collapse abilities-collapse';

    const summary = document.createElement('summary');
    summary.className = 'ic-section-head';
    summary.innerHTML =
      '<span class="ic-section-title">Abilities</span>' +
      '<span class="muted abilities-count"></span>';

    const body = document.createElement('div');
    body.className = 'ic-collapse-body abilities-body';

    const picker = document.createElement('div');
    picker.className = 'ability-picker';
    body.appendChild(picker);

    const foot = document.createElement('div');
    foot.className = 'abilities-foot';
    const saveBtn = document.createElement('button');
    saveBtn.type = 'button';
    saveBtn.className = 'small ability-save hidden';
    saveBtn.textContent = 'Save';
    const status = document.createElement('span');
    status.className = 'muted ability-status';
    // DOM order = visual order: Save first (left), status follows.
    foot.appendChild(saveBtn);
    foot.appendChild(status);
    body.appendChild(foot);

    tokenField.parentNode.insertBefore(details, tokenField);
    details.appendChild(summary);
    details.appendChild(body);

    function currentSelection() {
      return selectedAbilitiesFromPicker(picker);
    }
    function setCount(n) {
      const el = card.querySelector('.abilities-count');
      if (el) el.textContent = '(' + n + ')';
    }
    function sameSet(a, b) {
      if (a.length !== b.length) return false;
      const s = new Set(a);
      for (const x of b) if (!s.has(x)) return false;
      return true;
    }
    function onDirty() {
      const next = currentSelection();
      const pristine = t.abilities || [];
      const clean = sameSet(next, pristine);
      saveBtn.classList.toggle('hidden', clean);
      // Re-enable every time the button reappears — the click handler
      // disables it during the PATCH to prevent double-submit, and
      // without this the second flip shows a ghost button.
      if (!clean) saveBtn.disabled = false;
      status.textContent = '';
      status.className = 'muted ability-status';
    }
    function repaint() {
      renderAbilityPicker(picker, t.abilities || [], t.repo || '');
      setCount((t.abilities || []).length);
      saveBtn.classList.add('hidden');
      picker.querySelectorAll('.switch input[name="ability"]').forEach(cb => {
        cb.addEventListener('change', onDirty);
      });
    }
    saveBtn.addEventListener('click', async () => {
      const next = currentSelection();
      saveBtn.disabled = true;
      status.textContent = 'saving…';
      status.className = 'muted ability-status';
      try {
        await api.updateToken(t.token, { abilities: next });
        // Mutate the in-memory snapshot instead of calling
        // refreshTokens() — that re-fetches + re-renders the whole
        // grid, which closes every collapsible and reorders cards.
        // tokensCache holds the same `t` reference so downstream
        // reads (renderTokensGrid filter) see the new abilities.
        t.abilities = next;
        setCount(next.length);
        saveBtn.classList.add('hidden');
        status.textContent = 'saved';
      } catch (e) {
        status.textContent = e.message;
        status.className = 'error ability-status';
        saveBtn.disabled = false;
      }
    });

    repaint();
    return details;
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

      // ?user= pre-selects the account picker. ?repo= pre-selects the
      // repo in the single-select picker. Both live behind the same
      // "I arrived via a link on another page" pattern.
      const params = new URLSearchParams(location.search);
      const preUser = params.get('user');
      renderAccountPicker(preUser || '');
      const preRepo = params.get('repo');
      const repoHost = document.getElementById('issue-repo-host');
      renderRepoSelect(
        repoHost,
        preRepo && repoNames().includes(preRepo) ? preRepo : '',
        () => syncIssueAbilities(),
      );
      // Run once after the picker is rendered — the onChange hook
      // only fires on user interaction, not the initial value.
      syncIssueAbilities();
    } catch (e) {
      console.error(e);
    }
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('subscriptions', who);

    document.getElementById('issue-form').addEventListener('submit', async e => {
      e.preventDefault();
      const msg = document.getElementById('issue-msg');
      msg.textContent = '';
      const repoHost = document.getElementById('issue-repo-host');
      const repo = selectedRepoFromHost(repoHost);
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
      if (!repo) {
        msg.textContent = 'Pick a repo the key should bind to.';
        msg.className = 'error';
        return;
      }
      try {
        const t = await api.issueToken(scoped, repo, abilities);
        msg.textContent = 'Issued: ' + t.token;
        // Reset the repo + ability pickers but leave the account
        // picker alone — issuing several keys for the same holder is
        // a common pattern and retyping would be annoying.
        renderRepoSelect(repoHost, '');
        refreshAll();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refreshAll();
  })();
})();
