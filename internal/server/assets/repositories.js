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
  // destEditMode flips a card into the editor pane while the user is
  // adding or changing a destination. Cleared on save (the
  // dest-saved event handler) and on cancel; refreshRepos() rebuilds
  // the card from scratch and reads this map to decide which
  // fragment to render. Keyed by repo name; absence = no edit.
  const destEditMode = Object.create(null);

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

  function formatSyncTime(iso) {
    if (!iso) return '';
    try {
      const d = new Date(iso);
      if (isNaN(d.getTime())) return iso;
      return d.toLocaleString();
    } catch (_) { return iso; }
  }

  // formatRemoteRef renders one diverged-ref row in the detail
  // block. Local/remote SHAs are short-form; only_local shows just
  // the local SHA, only_remote just the remote, different shows both.
  function formatRemoteRef(r) {
    const ref = r.ref || '';
    const local = r.local ? shortSha(r.local) : '—';
    const remote = r.remote ? shortSha(r.remote) : '—';
    if (r.state === 'only_local')  return ref + '  local=' + local + '  remote=(missing)';
    if (r.state === 'only_remote') return ref + '  local=(missing)  remote=' + remote;
    if (r.state === 'different')   return ref + '  local=' + local + '  remote=' + remote;
    return ref + '  ' + r.state;
  }

  // destMode picks the fragment for the section's current state.
  // Edit mode wins when set even if a destination exists (the user
  // clicked Edit on an existing dest); otherwise an existing dest
  // shows the view, and absence shows the empty/Add prompt.
  function destMode(repoName) {
    if (destEditMode[repoName]) return 'edit';
    return destinationsByRepo[repoName] ? 'view' : 'empty';
  }

  // destViewModel returns the data the dest-* fragment renders
  // against. Each mode has its own view-model shape; the empty mode
  // doesn't need data, but returning {} keeps the GG.lazy.bind
  // contract simple. The class-toggle trick (`*_hidden`) covers the
  // "render only one of three sub-blocks" gates inside dest-view's
  // sync block, since the templating engine doesn't carry {{#if}}.
  function destViewModel(repoName) {
    const mode = destMode(repoName);
    if (mode === 'empty') return {};
    if (mode === 'view') {
      const d = destinationsByRepo[repoName];
      const status = d.last_sync_status || '';
      const when = d.last_sync_at ? formatSyncTime(d.last_sync_at) : '';
      const errText = d.last_sync_error ? d.last_sync_error.slice(0, 400) : '';

      // Remote-status block. Distinct from last-sync: last-sync = "did
      // our most recent push succeed", remote-status = "what does
      // ls-remote say is out there now". Four states drive four
      // pre-rendered blocks via the same *_hidden class-toggle trick
      // the sync block uses.
      const remoteStatus = d.remote_status || '';
      const remoteWhen = d.remote_checked_at ? formatSyncTime(d.remote_checked_at) : '';
      const remoteErr = d.remote_check_error ? d.remote_check_error.slice(0, 400) : '';
      const refs = Array.isArray(d.remote_refs) ? d.remote_refs : [];
      const diffRefs = refs.filter(r => r && r.state && r.state !== 'same');
      const remoteDetail = diffRefs.map(r => formatRemoteRef(r)).join('\n');
      // The remote-status field on the destination is just
      // "diverged" whenever any ref differs. The UI wants more nuance:
      // if EVERY differing ref is `only_local`, the remote is just
      // behind a local push (auto-mirror off + a client push) — Sync
      // now resolves it. If anything is `only_remote` or `different`,
      // there's a real two-sided fork worth flagging more loudly.
      const onlyLocalCount  = diffRefs.filter(r => r.state === 'only_local').length;
      const onlyRemoteCount = diffRefs.filter(r => r.state === 'only_remote').length;
      const differentCount  = diffRefs.filter(r => r.state === 'different').length;
      const allLocalOnly = diffRefs.length > 0 && onlyLocalCount === diffRefs.length;
      let remoteBadgeLabel = 'out of sync';
      let remoteBadgeClass = 'warn';
      let remoteHint = '';
      if (onlyRemoteCount > 0 || differentCount > 0) {
        remoteBadgeLabel = 'diverged';
      } else if (allLocalOnly) {
        remoteHint = 'Local has changes since the last sync. Click Sync now to push.';
      }
      return {
        url:           d.url || '',
        cred_label:    d.credential_name ? d.credential_name : '(no credential)',
        cred_missing:  d.credential_name ? '' : 'missing',
        enabled_class: d.enabled ? 'formidable' : 'empty',
        enabled_label: 'auto-mirror',
        enabled_title: d.enabled
          ? 'Click to disable automatic mirror-sync'
          : 'Click to enable automatic mirror-sync',
        when,
        err_text:        errText,
        err_body_hidden: errText ? '' : 'hidden',
        never_hidden:  status === ''   ? '' : 'hidden',
        ok_hidden:     status === 'ok' ? '' : 'hidden',
        err_hidden:    (status && status !== 'ok') ? '' : 'hidden',
        remote_when:              remoteWhen,
        remote_diff_count:        diffRefs.length,
        remote_detail:             remoteDetail,
        remote_badge_label:        remoteBadgeLabel,
        remote_badge_class:        remoteBadgeClass,
        remote_hint:               remoteHint,
        remote_hint_hidden:        remoteHint ? '' : 'hidden',
        remote_err_text:           remoteErr,
        remote_err_body_hidden:    remoteErr ? '' : 'hidden',
        remote_detail_hidden:      remoteDetail ? '' : 'hidden',
        remote_never_hidden:       remoteStatus === ''         ? '' : 'hidden',
        remote_in_sync_hidden:     remoteStatus === 'in_sync'  ? '' : 'hidden',
        remote_diverged_hidden:    remoteStatus === 'diverged' ? '' : 'hidden',
        remote_err_hidden:         remoteStatus === 'error'    ? '' : 'hidden',
      };
    }
    // edit
    const existing = destinationsByRepo[repoName] || null;
    return {
      title:          existing ? 'Edit mirror destination' : 'Add mirror destination',
      url:            existing ? existing.url : '',
      submit_label:   existing ? 'Save' : 'Add',
      privacy_hidden: existing ? 'hidden' : '',
      // Only the first 40 chars of the repo name go into the privacy
      // notice. Long names elide with the original here-doc behaviour.
      repo_short:     repoName.slice(0, 40),
    };
  }

  // applyDestSubmit sets/clears the data-lazy-submit family of attrs
  // on the host based on mode. POST in edit-mode-with-no-existing
  // (create), PATCH in edit-mode-with-existing. Other modes drop the
  // attrs so the helper doesn't try to wire submit triggers against
  // markup that doesn't have a form.
  function applyDestSubmit(host, repoName) {
    const mode = destMode(repoName);
    if (mode !== 'edit') {
      delete host.dataset.lazySubmit;
      delete host.dataset.lazySubmitMethod;
      delete host.dataset.lazyAfter;
      return;
    }
    const existing = destinationsByRepo[repoName] || null;
    const base = '/api/admin/repos/' + encodeURIComponent(repoName) + '/destinations';
    if (existing) {
      host.dataset.lazySubmit = base + '/' + encodeURIComponent(existing.id);
      host.dataset.lazySubmitMethod = 'PATCH';
    } else {
      host.dataset.lazySubmit = base;
      host.dataset.lazySubmitMethod = 'POST';
    }
    host.dataset.lazyAfter = 'event:dest-saved';
  }

  // setDestMode flips the per-repo edit toggle and re-renders the
  // section in place. Going INTO edit mode keeps the section open
  // so the form is immediately visible after Add / Edit; going OUT
  // (cancel) lands back on view or empty.
  function setDestMode(host, repoName, edit) {
    if (edit) destEditMode[repoName] = true;
    else delete destEditMode[repoName];
    host.dataset.lazyTpl = 'dest-' + destMode(repoName);
    applyDestSubmit(host, repoName);
    GG.lazy.refresh(host);
  }

  function renderDestinationSection(container, repoName) {
    // Entire section is one <details> disclosure. Summary carries
    // the title + a compact status hint (URL host or "not mirrored")
    // so the user can decide whether to expand without opening every
    // repo. Body lives in a single fragment whose name swaps as the
    // user moves through empty/view/edit.
    const dest = destinationsByRepo[repoName] || null;
    const statusHint = dest
      ? '<span class="ic-summary-hint mono">' + escapeHtml(shortenUrl(dest.url)) + '</span>'
      : '<span class="ic-summary-hint muted">not mirrored</span>';
    const openAttr = destOpenState[repoName] ? ' open' : '';

    container.innerHTML =
      '<details class="ic-collapse dest-details"' + openAttr + '>' +
        '<summary class="ic-section-head">' +
          '<span class="ic-section-title">Mirror destination</span>' +
          statusHint +
        '</summary>' +
      '</details>';

    const details = container.querySelector('.dest-details');
    details.dataset.repo = repoName;
    details.dataset.lazyTpl = 'dest-' + destMode(repoName);
    applyDestSubmit(details, repoName);
    details.addEventListener('toggle', () => { destOpenState[repoName] = details.open; });

    // dest-saved fires from GG.lazy after a successful POST/PATCH
    // (per data-lazy-after="event:dest-saved" set in applyDestSubmit).
    // Clear edit mode so the next render lands on view, then refresh
    // the page-level state so the new URL / credential / enabled
    // flags propagate to the summary hint and the chip filter row.
    details.addEventListener('dest-saved', () => {
      delete destEditMode[repoName];
      refreshRepos();
    });

    GG.lazy.bind(details, {
      getData: () => destViewModel(repoName),
      onRendered: host => wireDestState(host, container, repoName),
    });

    if (destOpenState[repoName]) GG.lazy.refresh(details);
  }

  // wireDestState attaches imperative behaviours that aren't carried
  // by data-lazy-submit — the buttons that don't post a form
  // (Sync now, Remove, enabled toggle, Add / Edit / Cancel) and the
  // GG.select / GG.toggle_switch placeholders inside the editor.
  // Called by GG.lazy after every render of the dest fragment, so
  // the wiring matches the just-rendered DOM (no stale references
  // from a previous mode).
  function wireDestState(host, container, repoName) {
    const mode = destMode(repoName);
    if (mode === 'empty') {
      const addBtn = host.querySelector('.add-dest-btn');
      if (addBtn) addBtn.addEventListener('click', () => {
        destOpenState[repoName] = true;
        setDestMode(host, repoName, true);
      });
      return;
    }
    if (mode === 'view') {
      wireDestView(host, repoName);
      return;
    }
    wireDestEdit(host, container, repoName);
  }

  function wireDestView(host, repoName) {
    const dest = destinationsByRepo[repoName];
    if (!dest) return;

    const editBtn = host.querySelector('.edit-dest-btn');
    if (editBtn) editBtn.addEventListener('click', () => {
      destOpenState[repoName] = true;
      setDestMode(host, repoName, true);
    });

    const removeBtn = host.querySelector('.remove-dest-btn');
    if (removeBtn) removeBtn.addEventListener('click', async () => {
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

    const syncBtn = host.querySelector('.sync-dest-btn');
    const syncMsg = host.querySelector('.dest-sync-msg');
    if (syncBtn && syncMsg) {
      syncBtn.addEventListener('click', async () => {
        syncBtn.disabled = true;
        syncMsg.textContent = 'pushing…';
        syncMsg.className = 'dest-sync-msg muted';
        try {
          const updated = await api.syncDestination(repoName, dest.id);
          destinationsByRepo[repoName] = updated;
          // Re-render the same host with updated data; no full
          // refreshRepos so other cards stay untouched.
          GG.lazy.refresh(host);
        } catch (e) {
          syncBtn.disabled = false;
          syncMsg.textContent = e.message;
          syncMsg.className = 'dest-sync-msg error';
        }
      });
    }

    // Refresh status — runs ls-remote against the destination, no
    // push. Same in-place refresh pattern as Sync now: re-render the
    // host with the updated dest, leave other cards alone.
    const refreshBtn = host.querySelector('.refresh-status-btn');
    if (refreshBtn && syncMsg) {
      refreshBtn.addEventListener('click', async () => {
        refreshBtn.disabled = true;
        syncMsg.textContent = 'checking…';
        syncMsg.className = 'dest-sync-msg muted';
        try {
          const updated = await api.refreshDestinationStatus(repoName, dest.id);
          destinationsByRepo[repoName] = updated;
          GG.lazy.refresh(host);
        } catch (e) {
          refreshBtn.disabled = false;
          syncMsg.textContent = e.message;
          syncMsg.className = 'dest-sync-msg error';
        }
      });
    }

    // Real toggle-switch (uses GG.toggle_switch). Replaces the prior
    // chip-styled button — same backend call, but the affordance now
    // looks like the switch it always was.
    const switchHost = host.querySelector('.enabled-switch-host');
    if (switchHost) {
      switchHost.innerHTML = GG.toggle_switch.html({
        checked: dest.enabled,
        ariaLabel: 'Enable automatic mirror-sync',
      });
      const switchEl = switchHost.querySelector('input[type="checkbox"]');
      GG.toggle_switch.onChange(switchEl, async (checked) => {
        switchEl.disabled = true;
        try {
          await api.updateDestination(repoName, dest.id, { enabled: checked });
          await refreshRepos();
        } catch (e) {
          switchEl.checked = !checked;
          switchEl.disabled = false;
          await GG.dialog.alert('Update failed', e.message);
        }
      });
    }
  }

  function wireDestEdit(host, container, repoName) {
    const existing = destinationsByRepo[repoName] || null;

    // Mount the credential select into its placeholder span. The
    // hidden input that GG.select projects has name="credential_name"
    // so GG.lazy.submit's [name] sweep picks it up automatically.
    const credHost = host.querySelector('.cred-select-host');
    if (credHost) {
      const credSelVal = existing ? (existing.credential_name || '') : '';
      const credOptions = credentialsCache.length === 0
        ? [{ value: '', label: '(no credentials in the vault. Add one first)', disabled: true }]
        : [{ value: '', label: 'Select a credential…' }]
            .concat(credentialsCache.map(c => ({ value: c.name, label: c.name })));
      credHost.innerHTML = GG.select.html({
        name: 'credential_name',
        value: credSelVal,
        placeholder: 'Select a credential…',
        options: credOptions,
      });
      GG.select.initAll(credHost);
    }

    // Privacy ack toggle is create-only (the privacy block hides
    // entirely on edit via privacy_hidden). The toggle's checkbox
    // has name="privacy_ack" so GG.lazy.submit collects it as part
    // of the body; the server validates the field on POST.
    const privacyHost = host.querySelector('.privacy-toggle-host');
    if (privacyHost) {
      privacyHost.innerHTML = GG.toggle_switch.html({
        name: 'privacy_ack',
        required: true,
        ariaLabel: 'Acknowledge destination privacy notice',
      });
    }

    const cancelBtn = host.querySelector('.cancel-btn');
    if (cancelBtn) cancelBtn.addEventListener('click', () => {
      setDestMode(host, repoName, false);
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
