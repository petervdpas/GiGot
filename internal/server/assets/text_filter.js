// GG.text_filter — substring search across rows. Sibling to
// GG.tag_filter: chips are for picking from a known vocabulary, this
// is for free-form search where every row has free-form fields
// (credential names, tag names, notes, identifiers, …) and the user
// just wants to type a few characters and narrow the list.
//
// Markup contract:
//
//   <div id="cred-filter" class="text-filter"></div>
//
// Page wires it via:
//
//   GG.text_filter.attachClientSide({
//     filterRow:    document.getElementById('cred-filter'),
//     placeholder:  'Search by name or notes…',
//     emptyHint:    'No credentials in the vault yet.',
//     rows:         () => credentialsCache,
//     rowText:      c => [c.name, c.notes, c.kind].filter(Boolean).join(' '),
//     renderRows:   visible => { /* paint */ },
//   });
//
// Behaviour:
//   - URL is the source of truth via `?q=<term>`. Deep-links and
//     copy-pasted URLs hydrate the input on load. Typing rewrites
//     the URL via replaceState (no back-stack pollution).
//   - Match is case-insensitive substring across whatever rowText
//     returns. Empty query → all rows visible.
//   - The summary line surfaces "<n> match" when the filter is
//     active so the admin sees how aggressive the cut is.
//
// Returned controller:
//   ctl.refresh()   — re-derive visible from rows() and re-render.
//                     Page calls this after rows mutate (created /
//                     deleted credential, etc).
//   ctl.value()     — read current query string.
//   ctl.setValue(v) — programmatic set + render (rare).

(function () {
  function paramQuery() {
    return (new URLSearchParams(location.search).get('q') || '').trim();
  }
  function setParamQuery(v) {
    const params = new URLSearchParams(location.search);
    if (v) params.set('q', v);
    else params.delete('q');
    const qs = params.toString();
    history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
  }

  function attachClientSide(opts) {
    const filterRow   = opts.filterRow;
    const placeholder = opts.placeholder || 'Search…';
    const emptyHint   = opts.emptyHint || '';
    const rows        = opts.rows || (() => []);
    const rowText     = opts.rowText || (() => '');
    const renderRows  = opts.renderRows || (() => {});
    if (!filterRow) return null;

    // Mount the input + summary chrome once. The page can call
    // ctl.refresh() repeatedly without tearing down the DOM, so
    // typing focus, caret position, and selection all survive
    // re-renders triggered by underlying data mutations.
    filterRow.innerHTML =
      '<div class="text-filter-row">' +
        '<input type="search" class="text-filter-input" placeholder="' +
          placeholder.replace(/"/g, '&quot;') + '" autocomplete="off">' +
        '<span class="text-filter-summary muted"></span>' +
      '</div>';

    const input = filterRow.querySelector('.text-filter-input');
    const summary = filterRow.querySelector('.text-filter-summary');
    input.value = paramQuery();

    function visibleRows() {
      const q = (input.value || '').trim().toLowerCase();
      if (!q) return rows();
      return rows().filter(r => {
        const text = (rowText(r) || '').toLowerCase();
        return text.includes(q);
      });
    }

    function renderAll() {
      const visible = visibleRows();
      renderRows(visible);
      const total = rows().length;
      const q = (input.value || '').trim();
      if (total === 0) {
        summary.textContent = emptyHint;
      } else if (!q) {
        summary.textContent = '';
      } else if (visible.length === 0) {
        summary.textContent = 'No matches.';
      } else if (visible.length === total) {
        summary.textContent = '';
      } else {
        summary.textContent = visible.length + ' of ' + total + ' match';
      }
    }

    input.addEventListener('input', () => {
      setParamQuery(input.value);
      renderAll();
    });

    return {
      refresh: renderAll,
      value: () => input.value,
      setValue: v => { input.value = v; setParamQuery(v); renderAll(); },
    };
  }

  window.GG = window.GG || {};
  window.GG.text_filter = { attachClientSide };
})();
