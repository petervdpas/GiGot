// GG.tag_picker — pill cluster + autocomplete dropdown for assigning
// tags to a repo, subscription, or account. The picker is purely
// presentational: it renders pills and reports a new explicit-tag
// list via onChange. The caller decides what API to hit (PUT
// /admin/repos/{name}/tags, PATCH /admin/tokens with tags, PUT
// /admin/accounts/.../tags).
//
// API:
//   GG.tag_picker.mount(host, {
//     tags: ["team:marketing", "env:prod"],          // explicit tags
//     allTags: ["team:marketing", "team:platform",   // catalogue (for picker)
//               "env:prod", "contractor:acme"],
//     inherited: [                                   // optional, for sub detail
//       { name: "team:marketing", source: "from repo" },
//       { name: "contractor:acme", source: "from bob@acme.com" },
//     ],
//     onChange: async (newTags) => { /* save */ },
//   });
//
// Inherited pills render muted with their `source` label and have no
// × button. Explicit pills are clickable to remove. The "+ add" button
// opens a dropdown listing every catalogue tag *not already explicitly
// applied*; selecting one calls onChange with the new tag appended.
// "Create new" lets the admin type a not-yet-known name.

(function () {
  const esc = (window.GG && window.GG.core && window.GG.core.escapeHtml) ||
    (s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c])));

  function mount(host, opts) {
    if (!host) return;
    opts = opts || {};
    const explicit = (opts.tags || []).slice();
    const inherited = opts.inherited || [];
    const allTags = (opts.allTags || []).slice();
    const onChange = opts.onChange || (() => {});

    function renderPills() {
      const pills = [];
      for (const inh of inherited) {
        pills.push(
          '<span class="tag-pill tag-pill-inherited" title="' + esc(inh.source) + '">' +
            esc(inh.name) +
            ' <span class="tag-pill-source">' + esc(inh.source) + '</span>' +
          '</span>'
        );
      }
      for (const name of explicit) {
        pills.push(
          '<span class="tag-pill" data-tag-name="' + esc(name) + '">' +
            esc(name) +
            ' <button type="button" class="tag-pill-remove" aria-label="Remove tag" data-remove="' + esc(name) + '">×</button>' +
          '</span>'
        );
      }
      return pills.join('');
    }

    function render() {
      host.innerHTML =
        '<div class="tag-picker">' +
          '<div class="tag-picker-pills">' + renderPills() + '</div>' +
          '<button type="button" class="tag-picker-add small">+ add tag</button>' +
        '</div>';

      host.querySelectorAll('.tag-pill-remove').forEach(btn => {
        btn.addEventListener('click', async () => {
          const name = btn.getAttribute('data-remove');
          const next = explicit.filter(t => t !== name);
          await commit(next);
        });
      });

      const addBtn = host.querySelector('.tag-picker-add');
      addBtn.addEventListener('click', e => {
        e.stopPropagation();
        openDropdown(addBtn);
      });
    }

    async function commit(next) {
      try {
        await onChange(next);
        // Mutate in place so re-renders read the new state.
        explicit.length = 0;
        next.forEach(t => explicit.push(t));
        render();
      } catch (e) {
        await GG.dialog.alert('Tag update failed', e.message || String(e));
      }
    }

    function openDropdown(anchor) {
      const explicitLower = new Set(explicit.map(t => t.toLowerCase()));
      const candidates = allTags.filter(t => !explicitLower.has(t.toLowerCase()));

      // Tear down any earlier dropdown the same picker might have left
      // open (rapid double-click on +add).
      host.querySelectorAll('.tag-picker-dropdown').forEach(el => el.remove());

      const dd = document.createElement('div');
      dd.className = 'tag-picker-dropdown';
      dd.innerHTML =
        '<input type="text" class="tag-picker-search" placeholder="Filter or type new...">' +
        '<div class="tag-picker-list">' +
          (candidates.length
            ? candidates.map(t => '<button type="button" class="tag-picker-option" data-add="' + esc(t) + '">' + esc(t) + '</button>').join('')
            : '<div class="muted tag-picker-empty">No more tags to add</div>') +
        '</div>' +
        '<button type="button" class="tag-picker-create small">Create &amp; add</button>';
      // Append to the .tag-picker (which is position: relative) so the
      // dropdown's `position: absolute` anchors *here*, not to whatever
      // distant positioned ancestor happens to be up the tree (the
      // sidebar, the body) — which is what produced the
      // "dropdown lands in the page corner" bug. The host element
      // itself has no positioning and varies per call site.
      const picker = host.querySelector('.tag-picker') || host;
      picker.appendChild(dd);

      const search = dd.querySelector('.tag-picker-search');
      const list = dd.querySelector('.tag-picker-list');
      const createBtn = dd.querySelector('.tag-picker-create');

      function close() {
        dd.remove();
        document.removeEventListener('click', outsideHandler, true);
        document.removeEventListener('keydown', escHandler, true);
      }
      function outsideHandler(ev) {
        if (!dd.contains(ev.target) && ev.target !== anchor) close();
      }
      function escHandler(ev) {
        if (ev.key === 'Escape') close();
      }
      // Defer attaching the outside-click handler until after this
      // tick so the click that opened the dropdown doesn't immediately
      // close it.
      setTimeout(() => {
        document.addEventListener('click', outsideHandler, true);
        document.addEventListener('keydown', escHandler, true);
      }, 0);

      search.addEventListener('input', () => {
        const q = search.value.trim().toLowerCase();
        list.querySelectorAll('.tag-picker-option').forEach(opt => {
          const name = (opt.getAttribute('data-add') || '').toLowerCase();
          opt.style.display = !q || name.includes(q) ? '' : 'none';
        });
      });

      list.querySelectorAll('.tag-picker-option').forEach(opt => {
        opt.addEventListener('click', async () => {
          const name = opt.getAttribute('data-add');
          close();
          await commit([...explicit, name]);
        });
      });

      async function tryCreate() {
        const name = search.value.trim();
        if (!name) return;
        // Prevent creating something that's already explicitly on this
        // entity — the admin probably meant to filter, not duplicate.
        if (explicitLower.has(name.toLowerCase())) {
          search.classList.add('error');
          return;
        }
        close();
        await commit([...explicit, name]);
      }
      createBtn.addEventListener('click', tryCreate);
      search.addEventListener('keydown', ev => {
        if (ev.key === 'Enter') {
          ev.preventDefault();
          tryCreate();
        }
      });
      search.focus();
    }

    render();
  }

  window.GG = window.GG || {};
  window.GG.tag_picker = { mount };
})();
