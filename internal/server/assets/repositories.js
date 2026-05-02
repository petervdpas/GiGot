// /admin/repositories page. Owns the repo grid, create-repo form,
// and the per-card Subscriptions + Mirror-destination sections. The
// subscriptions section uses tokensCache (fetched alongside repos) to
// paint chips; clicking "+ Issue key" navigates to the Subscription
// keys page with ?repo=<name> so it pre-selects there.

(function () {
  const Admin = window.Admin;
  const { api, escapeHtml, shortSha, initSidebar, guardSession } = Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let destinationsByRepo = {};
  let accountsCache = [];
  let tagsCatalogueCache = [];

  // subsOpenState: same pattern as destOpenState — preserves
  // expand/collapse per repo across refreshes so re-rendering the
  // repo grid doesn't fold every section the user opened.
  const subsOpenState = Object.create(null);

  function subscriptionsForRepo(name) {
    // Subscription keys bind to exactly one repo. The old multi-repo
    // shape is gone (§ one-repo-per-key migration), so this is a
    // direct equality check on t.repo.
    return tokensCache.filter(t => t.repo === name);
  }

  function renderRepoCard(r) {
    const card = document.createElement('div');
    card.className = 'info-card repo-card';
    card.dataset.repo = r.name;

    const badges = [];
    if (r.has_formidable) badges.push('<span class="badge formidable" title="Scaffolded as a Formidable context">Formidable</span>');
    if (r.empty) badges.push('<span class="badge empty" title="No commits yet — nothing has been pushed to this repo">empty</span>');

    const stats = [];
    const commitLabel = r.commits === 1 ? 'commit' : 'commits';
    stats.push('<span><span class="stat-label">' + commitLabel + '</span><span class="stat-value">' + r.commits + '</span></span>');
    if (!r.empty) {
      stats.push('<span><span class="stat-label">HEAD</span><span class="stat-value">' + escapeHtml(shortSha(r.head)) + '</span></span>');
      if (r.default_branch) {
        stats.push('<span><span class="stat-label">Branch</span><span class="stat-value">' + escapeHtml(r.default_branch) + '</span></span>');
      }
    }

    // "Convert to Formidable" is only meaningful for a repo that has
    // commits and doesn't already carry the marker. The server gates
    // the endpoint to formidable_first mode; in generic mode the
    // button would 403, so we hide it upfront rather than fail on click.
    const canConvert = !r.has_formidable && !r.empty;
    const convertBtn = canConvert
      ? '<button class="small secondary convert-formidable-btn">Convert to Formidable</button>'
      : '';

    card.innerHTML =
      '<div class="ic-header">' +
        '<div class="ic-title">' + escapeHtml(r.name) + '</div>' +
        '<div class="ic-chips">' + badges.join('') + '</div>' +
      '</div>' +
      '<div class="ic-stats">' + stats.join('') + '</div>' +
      '<div class="ic-section ic-tags-section">' +
        '<div class="ic-section-label muted">Tags</div>' +
        '<div class="ic-tags-host"></div>' +
      '</div>' +
      '<div class="ic-section" data-section="subs"></div>' +
      '<div class="ic-section" data-section="dest"></div>' +
      '<div class="ic-actions">' +
        convertBtn +
        '<button class="small danger delete-btn">Delete</button>' +
      '</div>';

    GG.tag_picker.mount(card.querySelector('.ic-tags-host'), {
      tags: r.tags || [],
      allTags: tagsCatalogueCache,
      onChange: async (next) => {
        const resp = await api.setRepoTags(r.name, next);
        // Mutate the cached row so a subsequent re-render reflects
        // the new state; no global refresh needed for a tag flip.
        r.tags = resp.tags || [];
      },
    });

    renderSubscriptionsSection(card.querySelector('[data-section="subs"]'), r.name);
    renderDestinationSection(card.querySelector('[data-section="dest"]'), r.name);

    const convertEl = card.querySelector('.convert-formidable-btn');
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

    card.querySelector('.delete-btn').addEventListener('click', async () => {
      const ok = await GG.dialog.confirm({
        title: 'Delete repository',
        message: 'Delete repo "' + r.name + '"?\n\nThis is destructive — the bare repo and any attached destinations are dropped.',
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

    return card;
  }

  // labelForSubscription returns the chip's visible text: the canonical
  // provider:identifier ("github:peter.vdpas@protonmail.com"). That's
  // the disambiguator an admin scans for — display names collide
  // across an org but the scoped identifier is unique. The full
  // human name moves into the tooltip (see chipTitle below).
  function labelForSubscription(t) {
    const acc = Admin.resolveAccount(t.username, accountsCache);
    if (acc) return escapeHtml(acc.provider + ':' + acc.identifier);
    return escapeHtml(t.username);
  }

  // chipTitle is the hover tooltip for a subscription chip — the
  // resolved account's full display name. Falls back to the scoped
  // username when the account row was deleted (legacy tokens).
  function chipTitle(t) {
    const acc = Admin.resolveAccount(t.username, accountsCache);
    return acc ? Admin.accountLabel(acc) : t.username;
  }

  function renderSubscriptionsSection(container, repoName) {
    const subs = subscriptionsForRepo(repoName);
    const open = subsOpenState[repoName] ? ' open' : '';

    // No summary hint: the expanded body already renders each
    // account as a chip, and duplicating names in the summary felt
    // redundant (per user feedback). Count alone is enough for the
    // collapsed row.
    container.innerHTML =
      '<details class="ic-collapse subs-details"' + open + '>' +
        '<summary class="ic-section-head">' +
          '<span class="ic-section-title">Subscriptions</span>' +
          '<span class="muted">(' + subs.length + ')</span>' +
        '</summary>' +
        '<div class="ic-collapse-body subs-body"></div>' +
      '</details>';

    const details = container.querySelector('.subs-details');
    details.addEventListener('toggle', () => { subsOpenState[repoName] = details.open; });

    const body = container.querySelector('.subs-body');
    body.innerHTML =
      (subs.length === 0
        ? '<div class="muted ic-section-empty">No subscription keys grant access to this repo.</div>'
        : '<div class="sub-chips">' +
            subs.map(s => {
              // Chip face is just the display name; the tooltip
              // carries name + provider:identifier so the
              // disambiguator (which provider account this is) is
              // one hover away without crowding the chip.
              return '<span class="sub-chip" title="' + escapeHtml(chipTitle(s)) + '">' +
                labelForSubscription(s) + '</span>';
            }).join('') +
          '</div>') +
      '<div class="subs-actions">' +
        '<button type="button" class="small secondary issue-key-btn">+ Issue key</button>' +
      '</div>';

    body.querySelector('.issue-key-btn').addEventListener('click', () => {
      // Cross-page nav: carry the repo through as a query param so the
      // Subscription keys page pre-selects it in the issue form. Used to
      // be pendingKeysRepo + panel switch; the URL is the seam now.
      location.href = '/admin/subscriptions?repo=' + encodeURIComponent(repoName);
    });
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
      ? [{ value: '', label: '(no credentials in the vault — add one first)', disabled: true }]
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
          'Git pushes plaintext commits to the destination — anyone with access to <code>' + escapeHtml(repoName.slice(0, 40)) + '</code> at that remote will be able to read every file and every past version of every file in this repository.' +
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

      document.getElementById('repo-count').textContent = repoData.count;
      const grid = document.getElementById('repo-grid');
      const empty = document.getElementById('repo-empty');
      grid.replaceChildren(...repoInfoCache.map(renderRepoCard));
      empty.classList.toggle('hidden', repoInfoCache.length !== 0);
    } catch (e) {
      console.error(e);
    }
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('repositories', who);

    // Render the Formidable-scaffold toggle into its placeholder on the
    // create form. Consistent with the other "toggle markup lives in
    // one JS helper" touchpoints.
    const scaffoldHost = document.getElementById('scaffold-host');
    if (scaffoldHost) {
      scaffoldHost.innerHTML =
        GG.toggle_switch.html({ name: 'scaffold', ariaLabel: 'Scaffold as Formidable context' }) +
        '<span class="control-label">Scaffold as Formidable context</span>';
    }

    document.getElementById('create-repo-form').addEventListener('submit', async e => {
      e.preventDefault();
      const f = e.target;
      const msg = document.getElementById('repo-msg');
      msg.textContent = '';
      msg.className = 'muted';
      try {
        await api.createRepo(f.name.value, f.scaffold.checked, f.source_url.value.trim());
        f.reset();
        await refreshRepos();
      } catch (ex) {
        msg.textContent = ex.message;
        msg.className = 'error';
      }
    });

    await refreshRepos();
  })();
})();
