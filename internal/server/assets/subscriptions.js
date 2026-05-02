// /admin/subscriptions page. Owns the Issue-key form and the token
// grid. Needs the repo list (for the repo picker + pre-select from
// ?repo=) and the credentials list (to decide whether the `mirror`
// ability is relevant — see KNOWN_ABILITIES below).

(function () {
  const Admin = window.Admin;
  const { api, escapeHtml, initSidebar, guardSession, copyToClipboard, accountLabel } = Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let accountsCache = [];
  let tagsCatalogueCache = [];

  function repoNames() { return repoInfoCache.map(r => r.name); }

  // Order: regulars (most common) → maintainers → admins (rarest).
  // accountOption comes from Admin so the label format stays
  // consistent with the sidebar / user strip. Any unknown future
  // role would silently drop out — keep this in sync with
  // internal/accounts/store.go KnownRoles.
  function accountOptionsSorted() {
    const regulars    = accountsCache.filter(a => a.role === 'regular').map(Admin.accountOption);
    const maintainers = accountsCache.filter(a => a.role === 'maintainer').map(Admin.accountOption);
    const admins      = accountsCache.filter(a => a.role === 'admin').map(Admin.accountOption);
    return regulars.concat(maintainers, admins);
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

  // accountRoleFor resolves a scoped username ("provider:identifier")
  // to the role on the matching Account, or null if no account exists
  // (legacy bare "alice" tokens from before the accounts model). Used
  // to decide which abilities are even legal to offer for this key —
  // the server fences `mirror` to admin + maintainer, so the UI must
  // match (see accounts.md §1).
  function accountRoleFor(scopedUsername) {
    if (!scopedUsername) return null;
    const acc = resolveAccountForToken(scopedUsername);
    return acc ? acc.role : null;
  }

  // KNOWN_ABILITIES mirrors internal/auth.KnownAbilities(). Each
  // entry has a `relevant({ accountRole })` predicate. An ability is
  // offered in the UI iff:
  //   - the global precondition holds (e.g. at least one credential
  //     in the vault to reference), AND
  //   - the issued account's role is allowed to hold the ability.
  //
  // Per-repo gates are deliberately absent: a repo with zero mirror
  // destinations is the *normal* starting state — gating mirror on
  // destination_count > 0 created a chicken-and-egg where the only
  // path to grant the bit went through the admin Repositories page
  // first. The role IS the structural fence.
  //
  // The server-side allowlist still rejects anything not named here
  // via 400, so this list must stay in lockstep with Go's list.
  const MIRROR_ROLES = new Set(['admin', 'maintainer']);

  const KNOWN_ABILITIES = [
    {
      name: 'mirror',
      hint: 'self-manage remote destinations',
      relevant: ({ accountRole }) =>
        credentialsCache.length > 0 && MIRROR_ROLES.has(accountRole),
    },
  ];

  function relevantAbilities(scopedUsername) {
    const accountRole = accountRoleFor(scopedUsername);
    return KNOWN_ABILITIES.filter(a => a.relevant({ accountRole }));
  }

  function renderAbilityPicker(container, selected, scopedUsername) {
    container.replaceChildren();
    const set = new Set(selected);
    const relevant = relevantAbilities(scopedUsername);
    const names = new Set(relevant.map(a => a.name));
    for (const a of relevant) {
      container.append(makeAbilityChip(a, set.has(a.name), false));
    }
    // Held but no longer relevant for THIS account — render muted so
    // the admin can revoke a stale bit on the current key without
    // rebinding. Common for keys issued before role fences existed.
    for (const name of set) {
      if (!names.has(name)) {
        container.append(makeAbilityChip(
          { name, hint: 'granted but not allowed for this account\'s role' },
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
    const usernameEl = document.querySelector(
      '#issue-account-host input[name="username"]'
    );
    const scoped = usernameEl ? usernameEl.value : '';
    const any = relevantAbilities(scoped).length > 0;
    wrap.classList.toggle('hidden', !any);
    if (any) renderAbilityPicker(picker, [], scoped);
  }

  // ─────────────────────────────────────────────────────────── token card

  // Wraps Admin.renderTokenCard with the admin-specific extras:
  // display-name-resolving title, legacy-account badge, and the
  // bind/edit/revoke actions. The card body (repos, abilities,
  // token + copy) is shared across /admin/subscriptions and /user,
  // so changes to that visual stay in one place.
  function renderTokenCard(t) {
    const resolved = resolveAccountForToken(t.username);
    // Title is just the display name — the provider:identifier
    // subtitle below already carries the email-shaped identifier in
    // full, so duplicating it as a muted suffix here is noise.
    const titleHTML = resolved
      ? escapeHtml(accountLabel(resolved))
      : escapeHtml(t.username);
    // Subtitle shows provider:identifier in full. The card header has
    // room for it, and clipping the email-form identifiers behind an
    // ellipsis hid the disambiguator (which provider account this key
    // belongs to) — exactly the thing admins scan for.
    const subtitle = resolved
      ? '<code class="acct-identifier-full" title="' + escapeHtml(t.username) + '">' +
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

    const card = Admin.renderTokenCard(t, { titleHTML, subtitle, leftChips, actions });
    installTagsSection(card, t);
    installAbilitiesSection(card, t);
    return card;
  }

  // installTagsSection adds a tag pill cluster between the chips row
  // and the abilities section. Pills come from t.tags (explicit) and
  // t.effective_tags - t.tags (inherited from repo + account). The
  // tag_picker mounts inline; saving calls api.updateToken with a
  // tags field, which mutates the in-memory token cache so a
  // subsequent re-render reflects state without a global refresh.
  function installTagsSection(card, t) {
    const explicit = t.tags || [];
    const explicitLower = new Set(explicit.map(s => s.toLowerCase()));
    const inheritedNames = (t.effective_tags || []).filter(
      n => !explicitLower.has(n.toLowerCase())
    );
    const inherited = inheritedNames.map(n => ({
      name: n,
      // Slice 2: source attribution is "inherited" without
      // distinguishing repo vs account. The pill carries the muted
      // styling so the admin sees it differs from explicit; clicking
      // through to the parent entity (repo or account) shows where
      // the tag lives.
      source: 'inherited',
    }));

    const tokenField = card.querySelector('.token-field');
    if (!tokenField) return;

    const section = document.createElement('div');
    section.className = 'ic-tags-section';
    section.innerHTML =
      '<div class="ic-section-label muted">Tags</div>' +
      '<div class="ic-tags-host"></div>';
    tokenField.parentNode.insertBefore(section, tokenField);

    GG.tag_picker.mount(section.querySelector('.ic-tags-host'), {
      tags: explicit.slice(),
      inherited,
      allTags: tagsCatalogueCache,
      onChange: async (next) => {
        await api.updateToken(t.token, { tags: next });
        // Mutate the in-memory snapshot so the next render of this
        // card sees the new state. Effective tags must be
        // recomputed — the picker re-mounts on its own using the
        // updated explicit list, but the inherited slice depends on
        // the difference, so we rewrite both arrays.
        t.tags = next.slice();
        const lower = new Set(next.map(s => s.toLowerCase()));
        const nextEffective = next.slice();
        for (const name of (t.effective_tags || [])) {
          if (!lower.has(name.toLowerCase())) nextEffective.push(name);
        }
        t.effective_tags = nextEffective;
      },
    });
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
      renderAbilityPicker(picker, t.abilities || [], t.username || '');
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
  // Flipping the account changes which abilities are legal to grant
  // (mirror is admin/maintainer-only), so we re-run the abilities
  // syncer on every change.
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
    const sel = host.querySelector('.gsel');
    if (sel) GG.select.init(sel, () => syncIssueAbilities());
  }

  // Full page refresh: repos + tokens + creds + accounts. Called on
  // boot and after issue/revoke/edit so every picker stays in sync.
  async function refreshAll() {
    try {
      const [repoData, tokenData, credData, acctData, tagData] = await Promise.all([
        api.listRepos().catch(() => ({ repos: [] })),
        api.listTokens().catch(() => ({ tokens: [] })),
        api.listCredentials().catch(() => ({ credentials: [] })),
        api.listAccounts().catch(() => ({ accounts: [] })),
        api.listTags().catch(() => ({ tags: [] })),
      ]);
      repoInfoCache = repoData.repos || [];
      tokensCache = tokenData.tokens || [];
      credentialsCache = credData.credentials || [];
      accountsCache = acctData.accounts || [];
      tagsCatalogueCache = (tagData.tags || []).map(t => t.name);
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
