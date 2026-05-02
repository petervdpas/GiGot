// GG.tag_filter — chip-filter UI + bulk-action wiring for any
// admin page that lists rows narrowed by tag. Symmetric to
// GG.tag_picker (which is the assignment surface on a single
// entity); this controller is the discovery / narrowing surface
// across many entities.
//
// Encapsulates everything that's generic about a tag-filter card:
//   - URL ↔ chip-state binding (?tag= as the source of truth)
//   - prefix-before-colon grouping of chips ("team:*" / "env:*" / "Other")
//   - pruning of selections that no row carries anymore
//   - visibility + label of the bulk-action row
//
// The controller is dumb about what's being filtered — the host
// page supplies a `getFilterableTags()` callback (the union of tag
// names some row carries) and a `getMatchCount()` callback (how
// many rows match the current selection). Chip clicks call
// `onSelectionChange()`; the bulk-action button calls `onAction()`.
//
// API:
//   const ctl = GG.tag_filter.mount({
//     filterRow,            // <div> host for the chip rows
//     actionsRow,           // <div> with the action button + summary
//     actionButton,         // <button> the action fires from
//     summary,              // <span> for "Filter: foo / no matches"
//     emptyHint,            // string shown when there is nothing to filter
//     actionLabel,          // (n) => "Revoke all matching (" + n + ")"
//     onSelectionChange,    // async () => { /* refetch filtered list */ }
//     onAction,             // async (selectedLowerNames) => {}
//     getFilterableTags,    // () => string[]   (display-cased names)
//     getMatchCount,        // () => number     (# of currently-visible rows)
//   });
//
//   ctl.render();           // (re-)paint chips + action row off the URL
//   ctl.selected();         // string[] (lower-cased) read off ?tag=
//   ctl.prune();            // drop ?tag= entries that aren't filterable
//                           //   any longer; returns true when state changed.

(function () {
  const esc = (window.GG && window.GG.core && window.GG.core.escapeHtml) ||
    (s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c])));

  // matches is the single predicate that decides whether a row
  // survives the chip filter. OR (union) semantics — chips are
  // inclusion filters, so a row matches iff any one of the row's
  // tags is in the selection. Empty `selectedLower` means "no
  // filter active" and matches everything. Exposed publicly via
  // `GG.tag_filter.matches` so call sites that need to recompute
  // the filtered slice off in-memory data (e.g. subscriptions.js
  // after a tag edit) don't grow their own copy of this predicate
  // — there's exactly one in-process definition of the rule.
  // The server-side equivalent lives in `effectiveCoversAny` in
  // handler_admin.go (Go runtime, same OR rule, same empty-list
  // semantics); cross-runtime by necessity, but kept in lockstep
  // by this comment + the design doc.
  function matches(rowTags, selectedLower) {
    if (!selectedLower || !selectedLower.length) return true;
    const have = new Set((rowTags || []).map(t => String(t).toLowerCase()));
    return selectedLower.some(s => have.has(s));
  }

  // selectedTagsFromURL parses ?tag= (repeating, lower-cased,
  // de-duped). Module-private; the public surface is ctl.selected().
  function selectedTagsFromURL() {
    const raw = new URLSearchParams(location.search).getAll('tag');
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

  // setSelectedTagsInURL writes the list back as `?tag=` params via
  // replaceState so the browser back-stack doesn't fill up with one
  // entry per chip click. Other params (?user=, ?repo=) are
  // preserved.
  function setSelectedTagsInURL(tagList) {
    const params = new URLSearchParams(location.search);
    params.delete('tag');
    for (const t of tagList) params.append('tag', t);
    const qs = params.toString();
    history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
  }

  // groupTagsByPrefix splits ["team:marketing", "env:prod", "legacy"]
  // into [["env", ["env:prod"]], ["team", ["team:marketing"]],
  // ["Other", ["legacy"]]]. Other is always last; the prefix
  // groups sort alphabetically; tags within a group sort by
  // localeCompare. Pure function — easy to unit test if we ever
  // grow tests for the JS layer.
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
    const ordered = [];
    for (const [k, v] of groups) if (k !== otherKey) ordered.push([k, v]);
    ordered.sort((a, b) => a[0].localeCompare(b[0]));
    if (groups.has(otherKey)) ordered.push([otherKey, groups.get(otherKey)]);
    return ordered;
  }

  function mount(opts) {
    const filterRow    = opts.filterRow;
    const actionsRow   = opts.actionsRow;
    const actionButton = opts.actionButton;
    const summary      = opts.summary;
    const emptyHint    = opts.emptyHint || 'No tags in use yet.';
    const actionLabel  = opts.actionLabel || (n => 'Action (' + n + ')');
    const getFilterableTags = opts.getFilterableTags || (() => []);
    const getMatchCount     = opts.getMatchCount || (() => 0);
    const onSelectionChange = opts.onSelectionChange || (async () => {});
    const onAction          = opts.onAction || (async () => {});

    function selected() {
      return selectedTagsFromURL();
    }

    // prune drops `?tag=` entries that no row carries anymore. The
    // host calls this right after refreshing the underlying data so
    // a stale chip can never linger as "Filter: hunniki / 0
    // matches" combo.
    function prune() {
      const filterable = new Set(
        getFilterableTags().map(n => n.toLowerCase()),
      );
      const current = selectedTagsFromURL();
      const kept = current.filter(s => filterable.has(s));
      if (kept.length === current.length) return false;
      setSelectedTagsInURL(kept);
      return true;
    }

    function render() {
      if (!filterRow) return;
      const sel = new Set(selectedTagsFromURL());
      const cataloguePresent = getFilterableTags();

      if (cataloguePresent.length === 0) {
        filterRow.innerHTML =
          '<div class="muted tag-filter-empty">' + esc(emptyHint) + '</div>';
        if (actionsRow) actionsRow.classList.add('hidden');
        return;
      }

      const grouped = groupTagsByPrefix(cataloguePresent);
      filterRow.innerHTML = grouped.map(([groupName, tags]) => {
        const chips = tags.map(name => {
          const lower = name.toLowerCase();
          const isSel = sel.has(lower);
          return '<button type="button" class="tag-chip' + (isSel ? ' selected' : '') +
                 '" data-tag="' + esc(lower) + '">' + esc(name) + '</button>';
        }).join('');
        return '<div class="tag-filter-row">' +
                 '<div class="tag-filter-group-label">' + esc(groupName) + '</div>' +
                 '<div class="tag-filter-group-chips">' + chips + '</div>' +
               '</div>';
      }).join('');

      filterRow.querySelectorAll('.tag-chip').forEach(btn => {
        btn.addEventListener('click', async () => {
          const tag = btn.getAttribute('data-tag');
          const next = new Set(selectedTagsFromURL());
          if (next.has(tag)) next.delete(tag);
          else next.add(tag);
          setSelectedTagsInURL([...next].sort());
          await onSelectionChange();
        });
      });

      if (!actionsRow || !actionButton || !summary) return;
      // The action row is visible iff at least one chip is selected
      // AND there is at least one matching row to act on. Anything
      // less is a "nothing to do" state and the destructive button
      // would be a permanent decoration that's worse than not having
      // it at all.
      const matchCount = getMatchCount();
      if (sel.size === 0 || matchCount === 0) {
        actionsRow.classList.add('hidden');
        return;
      }
      actionsRow.classList.remove('hidden');
      actionButton.textContent = actionLabel(matchCount);
      actionButton.disabled = false;
      summary.textContent = 'Filter: ' + [...sel].sort().join(' AND ');
    }

    if (actionButton) {
      actionButton.addEventListener('click', async () => {
        const sel = selectedTagsFromURL();
        if (!sel.length) return;
        await onAction(sel);
      });
    }

    return { render, selected, prune };
  }

  // attachClientSide is the higher-level helper for pages that filter
  // their rows in JS (repos, accounts — anywhere the API doesn't
  // accept ?tag= and the entire dataset is already in memory).
  // It does the wiring every such page would otherwise repeat:
  //
  //   - read every row's tag list via rowTags(row)
  //   - feed the union to the chip filter (getFilterableTags)
  //   - on chip click, recompute the visible subset (AND across
  //     selected chips, case-insensitive) and call renderRows
  //   - on caller-driven data change (e.g. tag picker on a card),
  //     a single ctl.refresh() prunes the URL filter, re-renders
  //     the chips, and re-renders the rows.
  //
  // Returned controller has just one method most callers care about:
  //   ctl.refresh()  → prune + chip render + row render
  //
  // For server-side-filter pages (subscriptions), reach for
  // mount() directly — the page owns the fetch + render flow.
  function attachClientSide(opts) {
    const filterRow    = opts.filterRow;
    const actionsRow   = opts.actionsRow || null;
    const actionButton = opts.actionButton || null;
    const summary      = opts.summary || null;
    const rows         = opts.rows || (() => []);
    const rowTags      = opts.rowTags || (() => []);
    const renderRows   = opts.renderRows || (() => {});
    const emptyHint    = opts.emptyHint || 'No tags in use yet.';
    const actionLabel  = opts.actionLabel;
    const onAction     = opts.onAction;

    function filterableTags() {
      const seen = new Map();
      for (const r of rows()) {
        for (const name of (rowTags(r) || [])) {
          const key = name.toLowerCase();
          if (!seen.has(key)) seen.set(key, name);
        }
      }
      return [...seen.values()];
    }

    function visibleRows() {
      const sel = ctl.selected();
      if (!sel.length) return rows();
      return rows().filter(r => matches(rowTags(r), sel));
    }

    function renderAll() {
      renderRows(visibleRows());
      ctl.render();
    }

    const ctl = mount({
      filterRow, actionsRow, actionButton, summary,
      emptyHint, actionLabel,
      getFilterableTags: filterableTags,
      getMatchCount:     () => visibleRows().length,
      onSelectionChange: async () => { renderAll(); },
      onAction:          onAction,
    });

    // Public refresh: caller runs this after a data mutation
    // (tag picker change, row added/removed). Prunes the URL
    // selection of any chip no row carries anymore, then re-renders
    // both the chips and the visible row list in one shot.
    function refresh() {
      ctl.prune();
      renderAll();
    }

    return Object.assign({}, ctl, { refresh, visibleRows });
  }

  window.GG = window.GG || {};
  window.GG.tag_filter = { mount, attachClientSide, matches };
})();
