// /admin/subscriptions page. Owns the Issue-key form and the token
// grid. Needs the repo list (for the repo picker + pre-select from
// ?repo=) and the credentials list (to decide whether the `mirror`
// ability is relevant — see KNOWN_ABILITIES below).

(function () {
  const Admin = window.Admin;
  const { api, escapeHtml, initSidebar, guardSession, copyToClipboard, accountLabel } = Admin;

  let repoInfoCache = [];
  let tokensCache = [];        // currently-visible subs (filtered by ?tag= chips)
  let allTokensCache = [];     // unfiltered subs — feeds the chip filter, since
                               // a chip is only useful if some sub actually carries
                               // it (directly or via repo/account inheritance).
  let credentialsCache = [];
  let accountsCache = [];
  // tagsCatalogueCache holds every tag name in the catalogue. Used
  // by the picker dropdown so an admin can re-attach a tag they
  // just orphaned. The CHIP FILTER does NOT read from here — chips
  // are filterable iff some sub's effective_tags carries them, which
  // is computed from allTokensCache.
  let tagsCatalogueCache = [];

  // filterableTagNames returns the union of effective tags across
  // every subscription in the unfiltered list — i.e. the set the
  // chip filter would actually be useful for. A tag in the catalogue
  // that isn't on any sub (directly or inherited) yields zero
  // matches, so showing it as a chip is misleading. This is the
  // "filter should only show tags it can actually filter on" rule.
  function filterableTagNames() {
    const seen = new Map();          // lower → display
    for (const t of (allTokensCache || [])) {
      for (const name of (t.effective_tags || [])) {
        const key = name.toLowerCase();
        if (!seen.has(key)) seen.set(key, name);
      }
    }
    return [...seen.values()];
  }

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
        // The PATCH response echoes back the canonical {tags,
        // effective_tags} so we can patch state in place — no full
        // card re-render needed (which would otherwise reset every
        // open abilities collapsible).
        const resp = await api.updateToken(t.token, { tags: next });
        await applyTokenTagUpdate(t, resp);
      },
    });
  }

  // abilityViewModel shapes the data the abilities fragment renders
  // against. Mirrors the rules in KNOWN_ABILITIES + relevantAbilities
  // (server-side abilities are always {name, hint}; the picker adds
  // `checked` / `stale` per-token state).
  //
  // - `checked`: empty string for unchecked, "checked" for checked
  //   (lets `<input … {{checked}}>` render correctly without an
  //   `{{#if}}` template feature we don't have).
  // - `stale`: empty string for normal, "stale" for "held but not
  //   relevant for this account's role" — drops in as a class on
  //   the row so existing CSS styles it muted.
  function abilityViewModel(t) {
    const accountRole = accountRoleFor(t.username);
    const held = new Set(t.abilities || []);
    const relevant = KNOWN_ABILITIES.filter(a => a.relevant({ accountRole }));
    const relevantNames = new Set(relevant.map(a => a.name));
    const items = relevant.map(a => ({
      name: a.name,
      hint: a.hint,
      checked: held.has(a.name) ? 'checked' : '',
      stale: '',
    }));
    // Held but no longer relevant (admin demoted to regular while
    // holding mirror). Render muted so the admin can revoke the
    // stale bit on the current key without rebinding.
    for (const name of held) {
      if (!relevantNames.has(name)) {
        items.push({
          name,
          hint: 'granted but not allowed for this account\'s role',
          checked: 'checked',
          stale: 'stale',
        });
      }
    }
    return { abilities: items };
  }

  // installAbilitiesSection drops the flat chip row that
  // Admin.renderTokenCard inserts and replaces it with a lazy
  // collapsible whose body is rendered through GG.lazy from the
  // `abilities` fragment. Toggles DON'T save on flip — they just
  // mark the section dirty. Save commits, then the section snaps
  // back to its pristine state WITHOUT re-rendering the card grid.
  //
  // The render lifecycle:
  //   1. <details> is built imperatively (only the chrome — the
  //      section title + the abilities count badge).
  //   2. GG.lazy.bind hooks the toggle event. On first open, the
  //      `abilities` fragment is rendered into the details body
  //      using abilityViewModel(t) as the data.
  //   3. onRendered wires dirty / save / status behaviour against
  //      the freshly rendered DOM. Because the body is rebuilt on
  //      each refresh(), all event listeners attach to whatever
  //      DOM is current — no stale references.
  function installAbilitiesSection(card, t) {
    const flatChips = card.querySelector(':scope > .ic-chips.cell-abilities');
    if (flatChips) flatChips.remove();
    const tokenField = card.querySelector('.token-field');
    if (!tokenField) return null;

    const details = document.createElement('details');
    details.className = 'ic-collapse abilities-collapse';
    details.dataset.lazyTpl = 'abilities';

    const summary = document.createElement('summary');
    summary.className = 'ic-section-head';
    summary.innerHTML =
      '<span class="ic-section-title">Abilities</span>' +
      '<span class="muted abilities-count">(' + (t.abilities || []).length + ')</span>';

    tokenField.parentNode.insertBefore(details, tokenField);
    details.appendChild(summary);

    function currentSelection() {
      return selectedAbilitiesFromPicker(details);
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

    GG.lazy.bind(details, {
      getData: host => abilityViewModel(t),
      // onRendered fires after each render() pass — both the first
      // open AND any explicit GG.lazy.refresh(host) call. We re-find
      // the foot elements because the prior render's nodes may have
      // been replaced.
      onRendered: host => {
        const saveBtn = host.querySelector('.ability-save');
        const status  = host.querySelector('.ability-status');
        if (!saveBtn || !status) return;

        function onDirty() {
          const next = currentSelection();
          const pristine = t.abilities || [];
          const clean = sameSet(next, pristine);
          saveBtn.classList.toggle('hidden', clean);
          if (!clean) saveBtn.disabled = false;
          status.textContent = '';
          status.className = 'muted ability-status';
        }
        host.querySelectorAll('.switch input[name="ability"]').forEach(cb => {
          cb.addEventListener('change', onDirty);
        });

        saveBtn.addEventListener('click', async () => {
          const next = currentSelection();
          saveBtn.disabled = true;
          status.textContent = 'saving…';
          status.className = 'muted ability-status';
          try {
            await api.updateToken(t.token, { abilities: next });
            // Mutate the in-memory snapshot instead of refetching;
            // tokensCache holds the same `t` reference so downstream
            // reads see the new abilities. No grid re-render — the
            // collapse stays open, other cards untouched.
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
      },
    });

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
  //
  // The chip filter UI itself lives in GG.tag_filter (assets/
  // tag_filter.js) — a controller symmetric to GG.tag_picker. This
  // page wires it up once via mountTagFilter() below; the page-level
  // refresh helpers just ask the controller to render() and prune().

  let tagFilterCtl = null;

  function mountTagFilter() {
    const filterRow    = document.getElementById('tag-filter');
    const actionsRow   = document.getElementById('tag-filter-actions');
    const actionButton = document.getElementById('btn-revoke-by-tag');
    const summary      = document.getElementById('tag-filter-summary');
    if (!filterRow || !actionsRow || !actionButton || !summary) return;
    tagFilterCtl = GG.tag_filter.mount({
      filterRow, actionsRow, actionButton, summary,
      emptyHint: 'No tags in use on any subscription yet — add one to a key, repo, or account and the chip will appear here.',
      actionLabel: n => 'Revoke all matching (' + n + ')',
      getFilterableTags: filterableTagNames,
      // tokensCache is the AND-filtered server response, so its
      // length is exactly the count we'd revoke.
      getMatchCount: () => tokensCache.length,
      onSelectionChange: refreshTokens,
      onAction: openRevokeByTagDialog,
    });
  }

  // tagFilterFromURL bridges the controller's URL state to the
  // server-query layer. Empty array means "no filter active";
  // listTokensByTags falls through to the unfiltered endpoint in
  // that case.
  function tagFilterFromURL() {
    return tagFilterCtl ? tagFilterCtl.selected() : [];
  }

  // applyTokenTagUpdate — the sub-card-picker-onChange path. Patches
  // the in-memory model from the PATCH response, then reconciles the
  // chip filter + visible grid IN PLACE: cards that stay don't get
  // re-rendered (so any open abilities collapsibles stay open), the
  // edited card just has its tag-picker re-mounted, cards that drop
  // out of the active filter are removed, and cards that newly match
  // are inserted. No full card grid re-render and no extra refetches.
  async function applyTokenTagUpdate(t, resp) {
    const newTags          = (resp && resp.tags) || [];
    const newEffectiveTags = (resp && resp.effective_tags) || [];
    // Update the row in tokensCache (the visible filtered set).
    t.tags = newTags.slice();
    t.effective_tags = newEffectiveTags.slice();
    // Mirror the update into allTokensCache so the chip filter
    // recomputes the right "filterable tags" set.
    const mirror = allTokensCache.find(x => x.token === t.token);
    if (mirror && mirror !== t) {
      mirror.tags = newTags.slice();
      mirror.effective_tags = newEffectiveTags.slice();
    } else if (!mirror) {
      // Defensive: a sub the server returned but we never had in
      // the unfiltered list (shouldn't happen at steady state).
      allTokensCache.push(t);
    }
    // Catalogue may have grown via auto-create. Append any names
    // we sent that weren't previously in the cache so the picker
    // dropdowns on other cards see them on next open.
    const have = new Set(tagsCatalogueCache.map(s => s.toLowerCase()));
    for (const name of newTags) {
      if (!have.has(name.toLowerCase())) {
        tagsCatalogueCache.push(name);
        have.add(name.toLowerCase());
      }
    }
    // Re-mount this card's tag picker so the inherited slice is
    // recomputed (effective − direct). Pills + dropdown reflect
    // the new state.
    remountCardTagPicker(t);
    // Chip filter: prune any URL selection that no sub carries
    // anymore, then repaint the chip row.
    if (tagFilterCtl) {
      tagFilterCtl.prune();
      tagFilterCtl.render();
    }
    // Re-derive tokensCache from the active filter against the
    // updated allTokensCache, then reconcile the grid in place.
    tokensCache = visibleFromFilter();
    reconcileTokenGrid();
    // Action-row state (visible / count) tracks tokensCache, so
    // the chip-filter render above already picked up the new count.
  }

  // visibleFromFilter computes the AND-filtered subset of
  // allTokensCache against the active ?tag= selection, exactly the
  // way the server's `?tag=` endpoint would. With no chip selected
  // the whole unfiltered list IS the visible list.
  function visibleFromFilter() {
    const sel = tagFilterCtl ? tagFilterCtl.selected() : [];
    if (!sel.length) return allTokensCache;
    return allTokensCache.filter(tok => {
      const have = new Set((tok.effective_tags || []).map(s => s.toLowerCase()));
      return sel.every(s => have.has(s));
    });
  }

  // remountCardTagPicker re-runs installTagsSection's mount logic on
  // a single card so its inherited-pill slice (effective − direct)
  // is recomputed against the freshly returned effective_tags. The
  // rest of the card's DOM (abilities collapse state in particular)
  // stays exactly as it was.
  function remountCardTagPicker(t) {
    const grid = document.getElementById('token-grid');
    const card = grid && grid.querySelector(
      '.info-card[data-token="' + cssEscape(t.token) + '"]',
    );
    if (!card) return;
    const oldSection = card.querySelector(':scope > .ic-tags-section');
    if (oldSection) oldSection.remove();
    installTagsSection(card, t);
  }

  // cssEscape returns a string safe for use inside an attribute
  // selector value. Falls through to native CSS.escape when
  // available; otherwise quotes the input naively (token strings
  // are URL-safe base64 so no special characters need escaping in
  // practice — this is belt-and-braces).
  function cssEscape(s) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(s);
    return String(s).replace(/"/g, '\\"');
  }

  // reconcileTokenGrid diffs the existing card DOM against
  // tokensCache and adjusts in place. Cards keyed by data-token:
  //   - present in DOM but missing from tokensCache → removed
  //   - present in tokensCache but missing from DOM → inserted at
  //     their tokensCache index
  //   - present in both → left untouched (preserves abilities
  //     collapse state, the whole point of this dance)
  // The order of remaining cards is left alone unless the visible
  // set fundamentally changed; cosmetic re-ordering on every tag
  // edit isn't worth the disturbance.
  function reconcileTokenGrid() {
    const grid = document.getElementById('token-grid');
    const empty = document.getElementById('token-empty');
    if (!grid) return;
    // Apply the ?user= local narrowing the same way renderTokensGrid
    // does, so reconcile + grid stay in lockstep on user-filtered
    // page loads.
    const scoped = userFilter();
    const visible = scoped
      ? tokensCache.filter(tok => tokenMatchesUser(tok, scoped))
      : tokensCache;
    const visibleSet = new Set(visible.map(tok => tok.token));
    // Drop cards that shouldn't be visible.
    for (const card of [...grid.querySelectorAll('.info-card[data-token]')]) {
      if (!visibleSet.has(card.dataset.token)) card.remove();
    }
    // Insert cards that should be visible but aren't yet, in their
    // tokensCache order. We insert by walking visible in order and
    // checking the card-at-that-index.
    const renderedTokens = new Set(
      [...grid.querySelectorAll('.info-card[data-token]')]
        .map(c => c.dataset.token),
    );
    for (let i = 0; i < visible.length; i++) {
      const tok = visible[i];
      if (renderedTokens.has(tok.token)) continue;
      const newCard = renderTokenCard(tok);
      const ref = grid.children[i] || null;
      if (ref) grid.insertBefore(newCard, ref);
      else grid.appendChild(newCard);
    }
    document.getElementById('count').textContent = visible.length;
    if (empty) empty.classList.toggle('hidden', visible.length !== 0);
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


  // applyPrunedFilteredFetch is the shared tail every refresh path
  // runs once allTokensCache is fresh. Three steps:
  //   1. ask the tag-filter controller to drop ?tag= entries no sub
  //      carries anymore (so a stale chip + zero-match revoke
  //      button can't linger after the last assignment was removed)
  //   2. fetch the filtered token list against the surviving
  //      selection (skipped when no chip is active — unfiltered
  //      list IS the visible list)
  //   3. render the card grid + chip filter row
  //
  // Has to run AFTER allTokensCache is set, since pruning reads
  // filterableTagNames() off it.
  async function applyPrunedFilteredFetch() {
    if (tagFilterCtl) tagFilterCtl.prune();
    const tagList = tagFilterFromURL();
    const filteredData = tagList.length
      ? await api.listTokensByTags(tagList).catch(() => ({ tokens: [] }))
      : null;
    tokensCache = filteredData
      ? (filteredData.tokens || [])
      : allTokensCache;
    renderTokensGrid();
    if (tagFilterCtl) tagFilterCtl.render();
  }

  // refreshTokens — the chip-toggle path. Hits the unfiltered
  // endpoint to refresh the chip set, then runs the shared
  // prune-then-fetch tail.
  async function refreshTokens() {
    try {
      const allData = await api.listTokensByTags([]);
      allTokensCache = allData.tokens || [];
      await applyPrunedFilteredFetch();
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

  // refreshAll — the boot / post-issue / post-revoke path. Pulls
  // everything the page needs in one parallel batch, then runs the
  // shared prune-then-fetch tail and re-renders the form pickers.
  async function refreshAll() {
    try {
      const [repoData, allTokenData, credData, acctData, tagData] = await Promise.all([
        api.listRepos().catch(() => ({ repos: [] })),
        api.listTokensByTags([]).catch(() => ({ tokens: [] })),
        api.listCredentials().catch(() => ({ credentials: [] })),
        api.listAccounts().catch(() => ({ accounts: [] })),
        api.listTags().catch(() => ({ tags: [] })),
      ]);
      repoInfoCache = repoData.repos || [];
      allTokensCache = allTokenData.tokens || [];
      credentialsCache = credData.credentials || [];
      accountsCache = acctData.accounts || [];
      tagsCatalogueCache = (tagData.tags || []).map(t => t.name);
      await applyPrunedFilteredFetch();

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

    // Mount the chip-filter controller. It binds the action button +
    // chip clicks itself; this page just feeds it data via the
    // getFilterableTags / getMatchCount callbacks and gives it the
    // refresh + revoke hooks.
    mountTagFilter();

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
