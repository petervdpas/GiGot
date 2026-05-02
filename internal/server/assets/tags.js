// /admin/tags page. Owns the tag catalogue: create, rename, delete.
// Assignments live on repo / subscription / account detail pages and
// land in slice 2 — the catalogue page is independent of those.

(function () {
  const { api, escapeHtml, initSidebar, guardSession } = window.Admin;

  function formatWhen(ts) {
    if (!ts) return '';
    try { return new Date(ts).toLocaleDateString(); } catch { return ts; }
  }

  // usagePill renders a small chip showing one entity-type usage count.
  // Zero counts collapse to a muted dash so the row reads "no
  // assignments yet" at a glance instead of "0 / 0 / 0" noise.
  function usagePill(label, n) {
    if (!n) return '<span class="muted">—</span>';
    return '<span class="tag-usage-pill">' + n + ' ' + label + '</span>';
  }

  // unusedTagNames returns the catalogue rows with zero references
  // across repos / subs / accounts. Used by the sweep dialog to show
  // exactly which tags will go before the admin commits — same
  // visibility-before-blast-radius pattern the cascade-delete dialog
  // uses for a single tag.
  function unusedTagNames(tags) {
    return (tags || []).filter(t => {
      const u = t.usage || {};
      return !u.repos && !u.subscriptions && !u.accounts;
    }).map(t => t.name);
  }

  async function refresh() {
    const data = await api.listTags();
    document.getElementById('tag-count').textContent = data.count;
    // The "Remove unused" button is enabled iff at least one
    // catalogue row has zero references; otherwise click would do
    // nothing, so the button reflects that as a disabled state.
    const sweepBtn = document.getElementById('btn-sweep-unused');
    if (sweepBtn) {
      const unused = unusedTagNames(data.tags);
      sweepBtn.disabled = unused.length === 0;
      sweepBtn.title = unused.length === 0
        ? 'No unused tags — every catalogue row has at least one assignment'
        : 'Remove ' + unused.length + ' tag' + (unused.length === 1 ? '' : 's') + ' with no assignments';
      sweepBtn._unused = unused;
    }
    const tbody = document.getElementById('tag-rows');
    tbody.replaceChildren(...data.tags.map(t => {
      const tr = document.createElement('tr');
      tr.innerHTML =
        '<td data-label="Name"><span class="tag-pill" data-tag-id="' + escapeHtml(t.id) + '">' + escapeHtml(t.name) + '</span></td>' +
        '<td data-label="Repos">' + usagePill('repos', t.usage.repos) + '</td>' +
        '<td data-label="Subs">' + usagePill('subs', t.usage.subscriptions) + '</td>' +
        '<td data-label="Accounts">' + usagePill('accounts', t.usage.accounts) + '</td>' +
        '<td data-label="Created" class="muted">' + escapeHtml(formatWhen(t.created_at)) + '</td>' +
        '<td data-label="By" class="muted">' + escapeHtml(t.created_by || '') + '</td>' +
        '<td class="row-actions"></td>';

      async function renameTag() {
        const next = await GG.dialog.prompt({
          title: 'Rename tag',
          message: 'New name for "' + t.name + '":',
          value: t.name,
          okText: 'Rename',
        });
        if (next == null) return;
        const trimmed = next.trim();
        if (!trimmed || trimmed === t.name) return;
        try {
          await api.renameTag(t.id, trimmed);
          await refresh();
        } catch (e) {
          await GG.dialog.alert('Rename failed', e.message);
        }
      }

      async function deleteTag() {
        // Cascade-delete is the design-doc default (§11 Q1) — make
        // the blast radius visible before the admin commits, since
        // every assignment row referencing this tag goes with it.
        const total = (t.usage.repos || 0) + (t.usage.subscriptions || 0) + (t.usage.accounts || 0);
        let message = 'Delete tag "' + t.name + '"?';
        if (total > 0) {
          const parts = [];
          if (t.usage.repos)         parts.push(t.usage.repos + ' repo' + (t.usage.repos === 1 ? '' : 's'));
          if (t.usage.subscriptions) parts.push(t.usage.subscriptions + ' subscription' + (t.usage.subscriptions === 1 ? '' : 's'));
          if (t.usage.accounts)      parts.push(t.usage.accounts + ' account' + (t.usage.accounts === 1 ? '' : 's'));
          message += '\n\nThis will also remove the tag from ' + parts.join(', ') + '.';
        }
        const ok = await GG.dialog.confirm({
          title: 'Delete tag',
          message,
          okText: 'Delete',
          dangerOk: true,
        });
        if (!ok) return;
        try {
          await api.deleteTag(t.id);
          await refresh();
        } catch (e) {
          await GG.dialog.alert('Delete failed', e.message);
        }
      }

      GG.row_menu.attach(tr.querySelector('.row-actions'), [
        { label: 'Rename', onClick: renameTag },
        { label: 'Delete', onClick: deleteTag, danger: true },
      ]);
      return tr;
    }));
  }

  (async function boot() {
    const who = await guardSession();
    if (!who) return;
    initSidebar('tags', who);

    // "Remove unused" sweep — confirm with the list of names before
    // firing, since the action is destructive (each removed row is a
    // catalogue row gone forever) even though it doesn't touch any
    // assignments. The dialog body shows up to 12 names inline; for
    // a sweep of 50+ tags we let the count carry the load and the
    // names truncate with an ellipsis hint.
    const sweepBtn = document.getElementById('btn-sweep-unused');
    if (sweepBtn) {
      sweepBtn.addEventListener('click', async () => {
        const unused = sweepBtn._unused || [];
        if (unused.length === 0) return;
        const preview = unused.length <= 12
          ? unused.join(', ')
          : unused.slice(0, 12).join(', ') + ' and ' + (unused.length - 12) + ' more';
        const ok = await GG.dialog.confirm({
          title: 'Remove unused tags',
          message: 'Remove ' + unused.length + ' tag' + (unused.length === 1 ? '' : 's') +
            ' with no assignments?\n\n' + preview +
            '\n\nThe tags themselves are deleted from the catalogue. This cannot be undone.',
          okText: 'Remove ' + unused.length,
          dangerOk: true,
        });
        if (!ok) return;
        try {
          await api.sweepUnusedTags();
          await refresh();
        } catch (e) {
          await GG.dialog.alert('Sweep failed', e.message);
        }
      });
    }

    // Add-tag form lives in a fragment rendered into the create-tag
    // drawer. GG.drawer.bindForm wires lazy + submit + close-on-
    // success + error-into-#tag-msg in one shot, so this page only
    // declares the API call and the post-success refresh.
    GG.drawer.bindForm('create-tag', {
      submit: async data => api.createTag((data.name || '').trim()),
      onSuccess: refresh,
    });
    GG.drawer.attachAll();

    await refresh();
  })();
})();
