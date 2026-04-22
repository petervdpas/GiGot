// /admin/repositories page. Owns the repo grid, create-repo form,
// and the per-card Subscriptions + Mirror-destination sections. The
// subscriptions section uses tokensCache (fetched alongside repos) to
// paint chips; clicking "+ Issue key" navigates to the Subscription
// keys page with ?repo=<name> so it pre-selects there.

(function () {
  const { api, escapeHtml, shortSha, initSidebar, guardSession } = window.Admin;

  let repoInfoCache = [];
  let tokensCache = [];
  let credentialsCache = [];
  let destinationsByRepo = {};

  function subscriptionsForRepo(name) {
    return tokensCache.filter(t => (t.repos || []).includes(name));
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
      '<div class="ic-section" data-section="subs"></div>' +
      '<div class="ic-section" data-section="dest"></div>' +
      '<div class="ic-actions">' +
        convertBtn +
        '<button class="small danger delete-btn">Delete</button>' +
      '</div>';

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

  function renderSubscriptionsSection(container, repoName) {
    const subs = subscriptionsForRepo(repoName);
    const header = '<div class="ic-section-head">' +
      '<span class="ic-section-title">Subscriptions</span>' +
      '<span class="muted">(' + subs.length + ')</span>' +
      '<button type="button" class="small secondary issue-key-btn">+ Issue key</button>' +
      '</div>';
    const body = subs.length === 0
      ? '<div class="muted ic-section-empty">No subscription keys grant access to this repo.</div>'
      : '<div class="sub-chips">' +
          subs.map(s => '<span class="sub-chip">' + escapeHtml(s.username) + '</span>').join('') +
        '</div>';
    container.innerHTML = header + body;
    container.querySelector('.issue-key-btn').addEventListener('click', () => {
      // Cross-page nav: carry the repo through as a query param so the
      // Subscription keys page pre-selects it in the issue form. Used to
      // be pendingKeysRepo + panel switch; the URL is the seam now.
      location.href = '/admin/subscriptions?repo=' + encodeURIComponent(repoName);
    });
  }

  function renderDestinationSection(container, repoName) {
    const dest = destinationsByRepo[repoName] || null;
    const header = '<div class="ic-section-head">' +
      '<span class="ic-section-title">Mirror destination</span>' +
      (dest ? '' : '<button type="button" class="small secondary add-dest-btn">+ Add</button>') +
      '</div>';
    if (!dest) {
      container.innerHTML = header +
        '<div class="muted ic-section-empty">Not mirrored. Add a destination to push this repo to an external git remote.</div>';
      container.querySelector('.add-dest-btn').addEventListener('click', () => {
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
    container.innerHTML = header +
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
      const [repoData, tokenData, credData] = await Promise.all([
        api.listRepos(),
        api.listTokens().catch(() => ({ tokens: [], count: 0 })),
        api.listCredentials().catch(() => ({ credentials: [], count: 0 })),
      ]);
      repoInfoCache = repoData.repos || [];
      tokensCache = tokenData.tokens || [];
      credentialsCache = credData.credentials || [];

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
