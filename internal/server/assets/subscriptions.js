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
        // Catalogue cache + filter chips must reflect tags the picker
        // just auto-created server-side. Without this, typing a new
        // name into the picker silently creates the catalogue row
        // but the chip filter at the top of the page stays stale
        // until a full reload — the user complaint that prompted this.
        await refreshTagCatalogueAndFilter();
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

  // renderTokensGrid paints the visible token cards into the grid.
  // Filtering by ?user= happens here (client-side narrowing); the
  // ?tag= filter is server-side, so tokensCache already contains the
  // intersected set when chips are active.
  function renderTokensGrid() {
    const scoped = userFilter();
    const visible = scoped ? tokensCache.filter(t => tokenMatchesUser(t, scoped)) : tokensCache;
    document.getElementById('count').textContent = visible.length;
    const grid = document.getElementById('token-grid');
    const empty = document.getElementById('token-empty');
    grid.replaceChildren(...visible.map(renderTokenCard));
    empty.classList.toggle('hidden', visible.length !== 0);
  }

  // ─────────────────────────────────────────────── tag filter (slice 3)

  // selectedTagsFromURL parses the active filter set off `?tag=` query
  // params (one per tag, repeating). The URL is the source of truth so
  // a deep-link from the Tags page or a copy-pasted URL hydrates the
  // filter state on load. Lower-cased + deduped.
  function selectedTagsFromURL() {
    const params = new URLSearchParams(location.search);
    const raw = params.getAll('tag');
    const seen = new Set();
    const out = [];
    for (const r of raw) {
      const v = (r || '').trim().toLowerCase();
      if (!v || seen.has(v)) continue;
      seen.add(v);
      out.push(v);
    }
    return out;
  }

  // setSelectedTagsInURL writes the next filter state back to ?tag=
  // params using replaceState so the browser back button doesn't fill
  // up with one entry per chip click. Other params (?user=, ?repo=)
  // are preserved.
  function setSelectedTagsInURL(tagList) {
    const params = new URLSearchParams(location.search);
    params.delete('tag');
    for (const t of tagList) params.append('tag', t);
    const qs = params.toString();
    history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
  }

  // groupTagsByPrefix splits ["team:marketing", "env:prod", "legacy"]
  // into { "team": [...], "env": [...], "Other": ["legacy"] }. Pure
  // function — same input → same shape, ordered keys come from the
  // first appearance in the input. The "Other" bucket sorts last in
  // the renderer, mirroring §5.5 of the design doc.
  function groupTagsByPrefix(names) {
    const groups = new Map();
    const otherKey = 'Other';
    for (const n of names) {
      const idx = n.indexOf(':');
      const key = idx > 0 ? n.slice(0, idx) : otherKey;
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(n);
    }
    for (const arr of groups.values()) arr.sort((a, b) => a.localeCompare(b));
    // Reorder so Other always renders last.
    const ordered = [];
    for (const [k, v] of groups) if (k !== otherKey) ordered.push([k, v]);
    ordered.sort((a, b) => a[0].localeCompare(b[0]));
    if (groups.has(otherKey)) ordered.push([otherKey, groups.get(otherKey)]);
    return ordered;
  }

  // renderTagFilter paints the chip grid + the active-action row.
  // tagsCatalogueCache is the source of truth for "what chips exist";
  // selected pulls from the URL. Toggling a chip rewrites the URL,
  // re-fetches the filtered listing, and repaints. Cards are painted
  // through the existing renderTokensGrid path so abilities-collapse
  // state on individual cards isn't disturbed by chip clicks.
  function renderTagFilter() {
    const host = document.getElementById('tag-filter');
    const actionsRow = document.getElementById('tag-filter-actions');
    const summary = document.getElementById('tag-filter-summary');
    const button = document.getElementById('btn-revoke-by-tag');
    if (!host || !actionsRow || !summary || !button) return;

    const selected = new Set(selectedTagsFromURL());
    const cataloguePresent = (tagsCatalogueCache || []).slice();
    // Stale chips: a tag in the URL that isn't in the catalogue (was
    // deleted, or arrived from a hand-edited URL). Surface them so
    // the user can deselect, even though they'll never match anything.
    for (const s of selected) {
      if (!cataloguePresent.some(t => t.toLowerCase() === s)) {
        cataloguePresent.push(s);
      }
    }

    if (cataloguePresent.length === 0) {
      host.innerHTML = '<div class="muted tag-filter-empty">No tags in the catalogue yet — create one on the Tags page or by typing it into a tag picker.</div>';
      actionsRow.classList.add('hidden');
      return;
    }

    const grouped = groupTagsByPrefix(cataloguePresent);
    const rows = grouped.map(([groupName, tags]) => {
      const chips = tags.map(name => {
        const lower = name.toLowerCase();
        const isSel = selected.has(lower);
        return '<button type="button" class="tag-chip' + (isSel ? ' selected' : '') +
               '" data-tag="' + escapeHtml(lower) + '">' + escapeHtml(name) + '</button>';
      }).join('');
      return '<div class="tag-filter-row">' +
               '<div class="tag-filter-group-label">' + escapeHtml(groupName) + '</div>' +
               '<div class="tag-filter-group-chips">' + chips + '</div>' +
             '</div>';
    }).join('');
    host.innerHTML = rows;

    host.querySelectorAll('.tag-chip').forEach(btn => {
      btn.addEventListener('click', () => {
        const tag = btn.getAttribute('data-tag');
        const next = new Set(selectedTagsFromURL());
        if (next.has(tag)) next.delete(tag);
        else next.add(tag);
        setSelectedTagsInURL([...next].sort());
        // After URL update, re-fetch + repaint. refreshTokens() reads
        // the URL via tagFilterFromURL and hits the filtered listing.
        refreshTokens();
      });
    });

    // Action row visibility tracks "any chip selected." Counts come
    // from the freshly fetched tokensCache (which is already
    // server-side filtered, so .length is the AND-filtered count).
    if (selected.size === 0) {
      actionsRow.classList.add('hidden');
    } else {
      actionsRow.classList.remove('hidden');
      const n = tokensCache.length;
      button.textContent = 'Revoke all matching (' + n + ')';
      button.disabled = n === 0;
      summary.textContent = n === 0
        ? 'No keys match the current filter.'
        : 'Filter: ' + [...selected].sort().join(' AND ');
    }
  }

  // tagFilterFromURL is the URL → server-query bridge: returns the
  // ?tag= list as an array of lower-cased names. Empty array means
  // "no filter active" — listTokensByTags falls through to the
  // unfiltered list endpoint in that case.
  function tagFilterFromURL() {
    return selectedTagsFromURL();
  }

  // refreshTagCatalogueAndFilter pulls a fresh /api/admin/tags listing
  // and re-renders the chip filter. Called after any tag-picker
  // onChange on this page so a name the picker just auto-created
  // (server-side, via PATCH) shows up as a chip immediately —
  // without it, the catalogue cache stays stale until a full
  // reload, which is exactly the "I added a tag and the filter
  // didn't grow" complaint. Cheap enough to call on every change
  // (one GET, one re-render of an at-most-N-chip grid).
  async function refreshTagCatalogueAndFilter() {
    try {
      const tagData = await api.listTags();
      tagsCatalogueCache = (tagData.tags || []).map(t => t.name);
      renderTagFilter();
    } catch (e) {
      console.error(e);
    }
  }

  // openRevokeByTagDialog enumerates the matching subs, asks the
  // admin to type the deterministic phrase the server expects, and
  // fires the bulk endpoint when the phrase matches. Server-side
  // checks the same phrase, so a typo at this layer is recoverable
  // — the call returns 400 instead of mass-revoking.
  async function openRevokeByTagDialog(selectedLower) {
    if (!selectedLower.length) return;
    const phrase = 'revoke ' + selectedLower.slice().sort().join(',');
    const matches = tokensCache.slice();
    if (matches.length === 0) {
      await GG.dialog.alert('Nothing to revoke', 'No keys match the current filter.');
      return;
    }
    // Build the dialog manually via createElement so we can render the
    // enumerate-then-confirm body with one card-list + one input. Pure
    // GG.dialog.confirm wouldn't fit the typed-phrase gate.
    const backdrop = document.createElement('div');
    backdrop.className = 'ed-dlg-backdrop';
    const dlg = document.createElement('div');
    dlg.className = 'ed-dlg';
    dlg.setAttribute('role', 'dialog');
    dlg.setAttribute('aria-modal', 'true');

    const rowsHTML = matches.map(t => {
      const abil = (t.abilities || []).map(a =>
        '<span class="ability-badge">' + escapeHtml(a) + '</span>').join('');
      return '<div class="revoke-by-tag-row">' +
        '<code>' + escapeHtml(t.username) + '</code> · ' +
        '<code>' + escapeHtml(t.repo) + '</code>' +
        (abil ? ' · ' + abil : '') +
      '</div>';
    }).join('');

    dlg.innerHTML =
      '<div class="ed-dlg-head">' +
        '<div class="ed-dlg-title">Revoke ' + matches.length + ' subscription' + (matches.length === 1 ? '' : 's') + '?</div>' +
      '</div>' +
      '<div class="ed-dlg-body">' +
        '<div class="ed-dlg-msg">' +
          'These keys will be revoked. Holders lose access immediately and the keys cannot be restored.' +
        '</div>' +
        '<div class="revoke-by-tag-list">' + rowsHTML + '</div>' +
        '<div class="ed-dlg-msg">' +
          'Type the phrase <span class="revoke-by-tag-phrase">' + escapeHtml(phrase) + '</span> to confirm.' +
        '</div>' +
        '<input class="ed-dlg-input" type="text" autocomplete="off" placeholder="' + escapeHtml(phrase) + '">' +
      '</div>' +
      '<div class="ed-dlg-foot">' +
        '<button type="button" class="ed-dlg-btn cancel">Cancel</button>' +
        '<button type="button" class="ed-dlg-btn ok danger" disabled>Revoke ' + matches.length + '</button>' +
      '</div>';

    backdrop.appendChild(dlg);
    document.body.appendChild(backdrop);
    const input = dlg.querySelector('.ed-dlg-input');
    const okBtn = dlg.querySelector('button.ok');
    const cancelBtn = dlg.querySelector('button.cancel');
    function close() {
      document.removeEventListener('keydown', onKey);
      backdrop.remove();
    }
    function onKey(e) {
      if (e.key === 'Escape') close();
    }
    backdrop.addEventListener('mousedown', e => { if (e.target === backdrop) close(); });
    cancelBtn.addEventListener('click', close);
    input.addEventListener('input', () => {
      okBtn.disabled = input.value.trim() !== phrase;
    });
    document.addEventListener('keydown', onKey);
    setTimeout(() => input.focus(), 0);

    okBtn.addEventListener('click', async () => {
      okBtn.disabled = true;
      okBtn.textContent = 'Revoking…';
      try {
        await api.revokeTokensByTag(selectedLower, phrase);
      } catch (e) {
        close();
        await GG.dialog.alert('Revoke failed', e.message || String(e));
        return;
      }
      close();
      await refreshAll();
    });
  }


  // refreshTokens hits the filtered listing endpoint when chips are
  // active so the AND-by-effective-tag rule is enforced server-side
  // (one source of truth for filter semantics; the chip + bulk-revoke
  // flows can't disagree). With no chips selected it's the plain
  // unfiltered listing — same call cost.
  async function refreshTokens() {
    try {
      const tagList = tagFilterFromURL();
      const data = await api.listTokensByTags(tagList);
      tokensCache = data.tokens || [];
      renderTokensGrid();
      renderTagFilter();
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
      const tagList = tagFilterFromURL();
      const [repoData, tokenData, credData, acctData, tagData] = await Promise.all([
        api.listRepos().catch(() => ({ repos: [] })),
        api.listTokensByTags(tagList).catch(() => ({ tokens: [] })),
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
      renderTagFilter();

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

    // Wire the bulk-revoke button. The button's enabled-when-non-empty
    // guard already lives in renderTagFilter; this listener just opens
    // the dialog with the current selection.
    const revokeBtn = document.getElementById('btn-revoke-by-tag');
    if (revokeBtn) {
      revokeBtn.addEventListener('click', () => {
        const sel = selectedTagsFromURL();
        if (sel.length) openRevokeByTagDialog(sel);
      });
    }

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
