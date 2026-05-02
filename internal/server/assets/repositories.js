// /admin/repositories page. Owns the repo grid, create-repo form,
// and the per-card Subscriptions + Mirror-destination sections. The
// subscriptions section uses tokensCache (fetched alongside repos) to
// paint chips; clicking "+ Issue key" navigates to the Subscription
// keys page with ?repo=<name> so it pre-selects there.

(function () {
  const Admin = window.Admin;
  const { api, escapeHtml, shortSha } = Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let destinationsByRepo = {};
  let accountsCache = [];
  let tagsCatalogueCache = [];
  // GG.tag_filter.attachClientSide controller. Owns chip rendering,
  // URL ↔ selection binding, prune-on-stale, and the AND-filter
  // computation; this page just hands it the data sources and
  // renderRows callback.
  let tagFilterCtl = null;

  // subsOpenState: same pattern as destOpenState — preserves
  // expand/collapse per repo across refreshes so re-rendering the
  // repo grid doesn't fold every section the user opened.
  const subsOpenState = Object.create(null);

  // repoOpenState tracks each repo card's outer collapse state. The
  // card itself is now a <details>; without persistence, every
  // refreshRepos() (which rebuilds the grid) would slam every open
  // card shut. Same shape as subsOpenState / destOpenState — keyed
  // by repo name.
  const repoOpenState = Object.create(null);

  function subscriptionsForRepo(name) {
    // Subscription keys bind to exactly one repo. The old multi-repo
    // shape is gone (§ one-repo-per-key migration), so this is a
    // direct equality check on t.repo.
    return tokensCache.filter(t => t.repo === name);
  }

  // repoCardViewModel shapes the JSON the repo-card-body fragment
  // renders against. Empty-state and conditional fields use the
  // class-toggle trick (no {{#if}} in the template engine), e.g.
  // an empty repo has no HEAD/branch so those rows hide via the
  // *_hidden classes.
  function repoCardViewModel(r) {
    const canConvert = !r.has_formidable && !r.empty;
    return {
      commit_label:   r.commits === 1 ? 'commit' : 'commits',
      commits:        r.commits,
      head:           shortSha(r.head),
      head_hidden:    r.empty ? 'hidden' : '',
      branch:         r.default_branch || '',
      branch_hidden:  (r.empty || !r.default_branch) ? 'hidden' : '',
      convert_hidden: canConvert ? '' : 'hidden',
    };
  }

  // renderRepoCard builds the outer <details> shell with the summary
  // (title + badges) imperatively, and binds GG.lazy to render the
  // body fragment on first open. The body contains stats + tags +
  // subs collapse + dest collapse + actions; onRendered wires the
  // imperative bits (tag picker, nested collapses, button click
  // handlers) against the freshly rendered DOM. Open/close state
  // persists per-repo across refreshes via repoOpenState.
  function renderRepoCard(r) {
    const card = document.createElement('details');
    card.className = 'info-card repo-card';
    card.dataset.repo = r.name;
    card.dataset.lazyTpl = 'repo-card-body';

    const badges = [];
    if (r.has_formidable) badges.push('<span class="badge formidable" title="Scaffolded as a Formidable context">Formidable</span>');
    if (r.empty) badges.push('<span class="badge empty" title="No commits yet. Nothing has been pushed to this repo">empty</span>');

    card.innerHTML =
      '<summary class="ic-header repo-card-head">' +
        '<div class="repo-card-title-wrap">' +
          '<span class="card-chevron" aria-hidden="true">▶</span>' +
          '<div class="ic-title">' + escapeHtml(r.name) + '</div>' +
        '</div>' +
        '<div class="ic-chips">' + badges.join('') + '</div>' +
      '</summary>';

    card.addEventListener('toggle', () => { repoOpenState[r.name] = card.open; });

    GG.lazy.bind(card, {
      getData: () => repoCardViewModel(r),
      onRendered: host => wireRepoCardBody(host, r),
    });

    // Restore prior open state across refreshes. Setting `open`
    // doesn't fire the toggle event, so we trigger the lazy render
    // directly to populate the body.
    if (repoOpenState[r.name]) {
      card.open = true;
      GG.lazy.refresh(card);
    }

    return card;
  }

  // wireRepoCardBody runs after each repo body fragment renders.
  // Mounts the tag picker, calls the inner collapse renderers, and
  // wires the convert / delete buttons. Same imperative work as the
  // old renderRepoCard, just deferred until the user opens the card.
  function wireRepoCardBody(host, r) {
    const tagsHost = host.querySelector('.ic-tags-host');
    if (tagsHost) {
      GG.tag_picker.mount(tagsHost, {
        tags: r.tags || [],
        allTags: tagsCatalogueCache,
        onChange: async (next) => {
          const resp = await api.setRepoTags(r.name, next);
          r.tags = resp.tags || [];
          try {
            const data = await api.listTags();
            tagsCatalogueCache = (data.tags || []).map(t => t.name);
          } catch { /* leave cache as-is */ }
          if (tagFilterCtl) tagFilterCtl.refresh();
        },
      });
    }

    const subsContainer = host.querySelector('[data-section="subs"]');
    if (subsContainer) renderSubscriptionsSection(subsContainer, r.name);
    const destContainer = host.querySelector('[data-section="dest"]');
    if (destContainer) renderDestinationSection(destContainer, r.name);

    // Convert-to-Formidable button is hidden via class for repos
    // that already carry the marker or are empty (the fragment uses
    // the convert_hidden class). The click handler still attaches —
    // if the button is hidden, the click never fires.
    const convertEl = host.querySelector('.convert-formidable-btn');
    if (convertEl) {
      convertEl.addEventListener('click', async () => {
        const ok = await GG.dialog.confirm({
          title: 'Stamp as Formidable context',
          message: 'Stamp "' + r.name + '" as a Formidable context?\n\nOne commit is added on top of HEAD carrying .formidable/context.json. Subsequent writes pick up structured record-merge behaviour.',
          okText: 'Stamp',
        });
        if (!ok) return;
        try {
          const res = await api.convertToFormidable(r.name);
          if (!res.stamped) {
            await GG.dialog.alert(
              'Already stamped',
              r.name + ' already carries a valid Formidable marker; no commit was written.'
            );
          }
          await refreshRepos();
        } catch (e) {
          await GG.dialog.alert('Stamp failed', e.message);
        }
      });
    }

    const deleteBtn = host.querySelector('.delete-btn');
    if (deleteBtn) {
      deleteBtn.addEventListener('click', async () => {
        const ok = await GG.dialog.confirm({
          title: 'Delete repository',
          message: 'Delete repo "' + r.name + '"?\n\nThis is destructive. The bare repo and any attached destinations are dropped.',
          okText: 'Delete',
          dangerOk: true,
        });
        if (!ok) return;
        try {
          await api.deleteRepo(r.name);
          await refreshRepos();
        } catch (e) {
          await GG.dialog.alert('Delete failed', e.message);
        }
      });
    }
  }

  // subscriptionViewModel shapes the JSON the repo-subscriptions
  // fragment renders against. Each chip carries its precomputed
  // visible label + hover-title (account.provider:identifier vs the
  // resolved display name) — same disambiguation that the imperative
  // version applied. Empty-state hiding via class trick (no {{#if}}
  // in the template engine, see lazy.md §4.3).
  function subscriptionViewModel(repoName) {
    const subs = subscriptionsForRepo(repoName).map(s => {
      const acc = Admin.resolveAccount(s.username, accountsCache);
      return {
        label: acc ? acc.provider + ':' + acc.identifier : s.username,
        title: acc ? Admin.accountLabel(acc) : s.username,
      };
    });
    return {
      subs,
      chips_hidden: subs.length === 0 ? 'hidden' : '',
      empty_hidden: subs.length === 0 ? '' : 'hidden',
    };
  }

  // renderSubscriptionsSection builds the <details> chrome (count in
  // the summary, persistent open/close state via subsOpenState) and
  // hands the body off to GG.lazy. The fragment renders the chip
  // list + the empty hint + the "+ Issue key" button; onRendered
  // wires the button to a cross-page nav (the Subscription keys
  // page reads ?repo= and pre-selects).
  function renderSubscriptionsSection(container, repoName) {
    const subs = subscriptionsForRepo(repoName);
    const open = subsOpenState[repoName] ? ' open' : '';

    container.innerHTML =
      '<details class="ic-collapse subs-details"' + open + '>' +
        '<summary class="ic-section-head">' +
          '<span class="ic-section-title">Subscriptions</span>' +
          '<span class="muted">(' + subs.length + ')</span>' +
        '</summary>' +
      '</details>';

    const details = container.querySelector('.subs-details');
    details.dataset.lazyTpl = 'repo-subscriptions';
    details.addEventListener('toggle', () => { subsOpenState[repoName] = details.open; });

    GG.lazy.bind(details, {
      getData: () => subscriptionViewModel(repoName),
      onRendered: host => {
        const btn = host.querySelector('.issue-key-btn');
        if (btn) {
          btn.addEventListener('click', () => {
            location.href = '/admin/subscriptions?repo=' + encodeURIComponent(repoName);
          });
        }
      },
    });

    // Restore prior open state: refresh()-induced re-renders rebuild
    // this DOM, so we re-trigger the lazy render if the user had it
    // open. Without this the body stays empty until the user clicks
    // the summary again.
    if (subsOpenState[repoName]) GG.lazy.refresh(details);
  }


  // destOpenState tracks per-repo open/closed state across a refresh.
  // Without it, refreshRepos() collapses any section the user had
  // expanded — re-rendering blows away the <details open> attribute.
  const destOpenState = Object.create(null);

  function renderDestinationSection(container, repoName) {
    const dest = destinationsByRepo[repoName] || null;

    // Entire section is one <details> disclosure. Summary carries the
    // title + a compact status hint (URL host or "— add one") so the
    // user can decide whether to expand without opening every repo.
    // Actions and the editor live in the body; the summary itself is
    // noise-free.
    const statusHint = dest
      ? '<span class="ic-summary-hint mono">' + escapeHtml(shortenUrl(dest.url)) + '</span>'
      : '<span class="ic-summary-hint muted">not mirrored</span>';
    const open = destOpenState[repoName] ? ' open' : '';

    container.innerHTML =
      '<details class="ic-collapse dest-details"' + open + '>' +
        '<summary class="ic-section-head">' +
          '<span class="ic-section-title">Mirror destination</span>' +
          statusHint +
        '</summary>' +
        '<div class="ic-collapse-body dest-body"></div>' +
      '</details>';

    const details = container.querySelector('.dest-details');
    details.addEventListener('toggle', () => { destOpenState[repoName] = details.open; });

    const body = container.querySelector('.dest-body');

    if (!dest) {
      body.innerHTML =
        '<div class="dest-empty-row">' +
          '<span class="muted">Not mirrored. Add a destination to push this repo to an external git remote.</span>' +
          '<button type="button" class="small secondary add-dest-btn">+ Add destination</button>' +
        '</div>';
      body.querySelector('.add-dest-btn').addEventListener('click', () => {
        // Editor replaces the whole container — re-open when we come
        // back to this view so the user doesn't have to click twice.
        destOpenState[repoName] = true;
        renderDestinationEditor(container, repoName, null);
      });
      return;
    }

    const credPill = dest.credential_name
      ? '<span class="cred-pill">' + escapeHtml(dest.credential_name) + '</span>'
      : '<span class="cred-pill missing">(no credential)</span>';
    // Enabled/disabled is a click-to-toggle button on the display row —
    // pause/resume is a management gesture on an existing destination,
    // not a field on the create form.
    const enabledBadge = dest.enabled
      ? '<button type="button" class="badge formidable enabled-toggle" title="Click to disable automatic mirror-sync">enabled</button>'
      : '<button type="button" class="badge empty enabled-toggle" title="Click to enable automatic mirror-sync">disabled</button>';
    body.innerHTML =
      '<div class="dest-row">' +
        '<div class="dest-url"><span class="stat-label">URL</span> <code>' + escapeHtml(dest.url) + '</code></div>' +
        '<div class="dest-meta">' +
          '<span class="stat-label">Credential</span> ' + credPill + ' ' + enabledBadge +
        '</div>' +
        renderDestSyncBlock(dest) +
        '<div class="dest-actions">' +
          '<button type="button" class="small sync-dest-btn">Sync now</button>' +
          '<button type="button" class="small secondary edit-dest-btn">Edit</button>' +
          '<button type="button" class="small danger remove-dest-btn">Remove</button>' +
          '<span class="dest-sync-msg muted"></span>' +
        '</div>' +
      '</div>';

    container.querySelector('.edit-dest-btn').addEventListener('click', () => {
      destOpenState[repoName] = true;
      renderDestinationEditor(container, repoName, dest);
    });
    container.querySelector('.remove-dest-btn').addEventListener('click', async () => {
      const ok = await GG.dialog.confirm({
        title: 'Remove mirror destination',
        message: 'Remove mirror destination from "' + repoName + '"?',
        okText: 'Remove',
        dangerOk: true,
      });
      if (!ok) return;
      try {
        await api.deleteDestination(repoName, dest.id);
        await refreshRepos();
      } catch (e) {
        await GG.dialog.alert('Remove failed', e.message);
      }
    });
    const syncBtn = container.querySelector('.sync-dest-btn');
    const syncMsg = container.querySelector('.dest-sync-msg');
    syncBtn.addEventListener('click', async () => {
      syncBtn.disabled = true;
      syncMsg.textContent = 'pushing…';
      syncMsg.className = 'dest-sync-msg muted';
      try {
        const updated = await api.syncDestination(repoName, dest.id);
        destinationsByRepo[repoName] = updated;
        renderDestinationSection(container, repoName);
      } catch (e) {
        syncBtn.disabled = false;
        syncMsg.textContent = e.message;
        syncMsg.className = 'dest-sync-msg error';
      }
    });

    const toggleBtn = container.querySelector('.enabled-toggle');
    toggleBtn.addEventListener('click', async () => {
      toggleBtn.disabled = true;
      try {
        await api.updateDestination(repoName, dest.id, { enabled: !dest.enabled });
        await refreshRepos();
      } catch (e) {
        toggleBtn.disabled = false;
        await GG.dialog.alert('Update failed', e.message);
      }
    });
  }

  // shortenUrl produces a compact summary hint like "github.com/org/repo"
  // so the collapsed summary is informative at a glance without wrapping.
  function shortenUrl(url) {
    if (!url) return '';
    try {
      const u = new URL(url);
      return u.host + u.pathname.replace(/\.git$/, '');
    } catch {
      return url;
    }
  }

  function renderDestSyncBlock(dest) {
    if (!dest.last_sync_status) {
      return '<div class="dest-sync dest-sync-never"><span class="stat-label">Last sync</span> <span class="muted">never</span></div>';
    }
    const when = dest.last_sync_at ? formatSyncTime(dest.last_sync_at) : '';
    if (dest.last_sync_status === 'ok') {
      return '<div class="dest-sync dest-sync-ok">' +
        '<span class="stat-label">Last sync</span> ' +
        '<span class="badge formidable">ok</span> ' +
        '<span class="muted">' + escapeHtml(when) + '</span>' +
        '</div>';
    }
    const errText = dest.last_sync_error ? escapeHtml(dest.last_sync_error).slice(0, 400) : '';
    return '<div class="dest-sync dest-sync-err">' +
      '<span class="stat-label">Last sync</span> ' +
      '<span class="badge warn">error</span> ' +
      '<span class="muted">' + escapeHtml(when) + '</span>' +
      (errText ? '<pre class="dest-sync-err-body">' + errText + '</pre>' : '') +
      '</div>';
  }

  function formatSyncTime(iso) {
    if (!iso) return '';
    try {
      const d = new Date(iso);
      if (isNaN(d.getTime())) return iso;
      return d.toLocaleString();
    } catch (_) { return iso; }
  }

  function renderDestinationEditor(container, repoName, existing) {
    const isEdit = !!existing;
    const credSelVal = existing ? (existing.credential_name || '') : '';
    const credOptions = credentialsCache.length === 0
      ? [{ value: '', label: '(no credentials in the vault. Add one first)', disabled: true }]
      : [{ value: '', label: 'Select a credential…' }]
          .concat(credentialsCache.map(c => ({ value: c.name, label: c.name })));
    const credSelHtml = GG.select.html({
      name: 'credential_name',
      value: credSelVal,
      placeholder: 'Select a credential…',
      options: credOptions,
    });
    const urlVal = existing ? escapeHtml(existing.url) : '';
    // Privacy consent per remote-sync.md §3.7: required on every new
    // destination. Edits skip the re-prompt — the admin already
    // consented when it was created.
    const privacyBlock = isEdit ? '' :
      '<div class="dest-privacy">' +
        '<div class="dest-privacy-warn">' +
          '<strong>Privacy notice.</strong> ' +
          'Adding a mirror destination turns off GiGot\'s sealed-body advantage for this repo. ' +
          'Git pushes plaintext commits to the destination. Anyone with access to <code>' + escapeHtml(repoName.slice(0, 40)) + '</code> at that remote will be able to read every file and every past version of every file in this repository.' +
        '</div>' +
        '<div class="switch-row">' +
          GG.toggle_switch.html({ name: 'privacy_ack', required: true, ariaLabel: 'Acknowledge destination privacy notice' }) +
          '<span class="control-label">I understand the contents of this repo will be readable at the destination.</span>' +
        '</div>' +
      '</div>';
    container.innerHTML =
      '<div class="ic-section-head">' +
        '<span class="ic-section-title">' + (isEdit ? 'Edit mirror destination' : 'Add mirror destination') + '</span>' +
      '</div>' +
      '<form class="dest-form">' +
        '<label class="dest-field"><span class="stat-label">URL</span>' +
          '<input type="text" name="url" value="' + urlVal + '" placeholder="https://github.com/org/repo.git" required>' +
        '</label>' +
        '<div class="dest-field"><span class="stat-label">Credential</span>' +
          credSelHtml +
        '</div>' +
        privacyBlock +
        '<div class="dest-actions">' +
          '<button type="submit" class="small">' + (isEdit ? 'Save' : 'Add') + '</button>' +
          '<button type="button" class="small secondary cancel-btn">Cancel</button>' +
          '<span class="dest-err error"></span>' +
        '</div>' +
      '</form>';
    GG.select.initAll(container);
    const form = container.querySelector('.dest-form');
    const err = form.querySelector('.dest-err');
    form.querySelector('.cancel-btn').addEventListener('click', () => {
      renderDestinationSection(container, repoName);
    });
    form.addEventListener('submit', async e => {
      e.preventDefault();
      err.textContent = '';
      const body = {
        url: form.url.value.trim(),
        credential_name: form.credential_name.value,
      };
      try {
        if (isEdit) {
          await api.updateDestination(repoName, existing.id, body);
        } else {
          await api.createDestination(repoName, body);
        }
        await refreshRepos();
      } catch (ex) {
        err.textContent = ex.message;
      }
    });
  }

  async function refreshRepos() {
    try {
      const [repoData, tokenData, credData, acctData, tagData] = await Promise.all([
        api.listRepos(),
        api.listTokens().catch(() => ({ tokens: [], count: 0 })),
        api.listCredentials().catch(() => ({ credentials: [], count: 0 })),
        api.listAccounts().catch(() => ({ accounts: [] })),
        api.listTags().catch(() => ({ tags: [] })),
      ]);
      repoInfoCache = repoData.repos || [];
      tokensCache = tokenData.tokens || [];
      credentialsCache = credData.credentials || [];
      accountsCache = acctData.accounts || [];
      tagsCatalogueCache = (tagData.tags || []).map(t => t.name);

      // Destinations are admin-scoped per-repo — one fetch per repo is
      // fine at admin workloads, and keeps the public /api/repos
      // response free of admin-only fields.
      const destEntries = await Promise.all(repoInfoCache.map(async r => {
        try {
          const d = await api.listDestinations(r.name);
          return [r.name, (d.destinations || [])[0] || null];
        } catch {
          return [r.name, null];
        }
      }));
      destinationsByRepo = Object.fromEntries(destEntries);

      // refresh() prunes the URL filter against the new data, then
      // re-renders both the chips and the visible-row list (which
      // is what calls renderRepoCard for each surviving row).
      if (tagFilterCtl) tagFilterCtl.refresh();
    } catch (e) {
      console.error(e);
    }
  }

  (async function boot() {
    if (!(await Admin.bootPage('repositories'))) return;
    GG.drawer.declareAll([
      { name: 'create-repository', title: 'Create repository' },
    ]);

    // Mount the tag filter once — the controller owns the chip
    // rendering, URL state, and AND-filter computation. The page
    // supplies data sources (rows + rowTags) and a renderRows
    // callback that paints the visible cards into the grid.
    tagFilterCtl = GG.tag_filter.attachClientSide({
      filterRow: document.getElementById('tag-filter'),
      emptyHint: 'No tags in use on any repository yet. Add one to a card and the chip will appear here.',
      rows:    () => repoInfoCache,
      rowTags: r => r.tags || [],
      renderRows: visible => {
        const grid = document.getElementById('repo-grid');
        const empty = document.getElementById('repo-empty');
        grid.replaceChildren(...visible.map(renderRepoCard));
        // Count tracks visible (filtered) — same as
        // /admin/subscriptions where (N) reflects the visible set.
        document.getElementById('repo-count').textContent = visible.length;
        empty.classList.toggle('hidden', visible.length !== 0);
      },
    });

    // Create-repository form lives in a fragment rendered into the
    // create-repository drawer. GG.drawer.bindForm wires lazy +
    // submit + close + error-into-#repo-msg in one shot. The
    // Formidable-scaffold toggle is mounted in onRendered because
    // it's a GG.toggle_switch placeholder that needs imperative
    // initialisation after each render.
    GG.drawer.bindForm('create-repository', {
      onRendered: host => {
        // Just the toggle pill — the surrounding .switch-row chrome
        // (label + hint) lives in the fragment so the markup matches
        // the .switch-row pattern used by the abilities picker on
        // /admin/subscriptions. Same look for every "labeled toggle
        // in a drawer row" surface.
        const scaffoldHost = host.querySelector('#scaffold-host');
        if (scaffoldHost) {
          scaffoldHost.innerHTML = GG.toggle_switch.html({
            name: 'scaffold',
            ariaLabel: 'Scaffold as Formidable context',
          });
        }
      },
      submit: async data => {
        const name = (data.name || '').trim();
        if (!name) throw new Error('Name is required.');
        // collectFormData returns single checkboxes as booleans.
        return api.createRepo(name, data.scaffold === true, (data.source_url || '').trim());
      },
      onSuccess: refreshRepos,
    });
    GG.drawer.attachAll();

    await refreshRepos();
  })();
})();
