// GG.dialog — backdrop-modal replacements for the native alert(),
// confirm() and prompt() dialogs. Ported from goop2's ui/assets/js
// and extended as GiGot needs surfaced.
//
// API:
//   await GG.dialog.alert("Title", "Message body");          // → void
//   await GG.dialog.confirm("Are you sure?", "Title");        // → true|false
//   await GG.dialog.confirm({                                  // full form
//     title: "Delete key?",
//     message: "This cannot be undone.",
//     okText: "Delete",
//     cancelText: "Cancel",
//     dangerOk: true,
//   });
//   await GG.dialog.prompt({                                   // → string|null
//     title: "Display name for alice",
//     message: "Shown in the sidebar and subscription cards.",
//     defaultValue: "Peter van de Pas",
//     placeholder: "e.g. Alice Example",
//     okText: "Save",
//     password: false, // true → <input type=password>
//   });

(function () {
  const esc = (window.GG && window.GG.core && window.GG.core.escapeHtml) ||
    (s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c])));

  function createElement(html) {
    const t = document.createElement('template');
    t.innerHTML = html.trim();
    return t.content.firstElementChild;
  }

  function dialog(opts) {
    opts = opts || {};
    const title = opts.title || '';
    const message = opts.message || '';
    const okLabel = opts.okText || 'OK';
    const cancelLabel = opts.cancel === false ? null : (opts.cancelText || 'Cancel');
    const dangerOk = !!opts.dangerOk;
    const hideOk = opts.ok === false;

    return new Promise(function (resolve) {
      const backdrop = createElement('<div class="ed-dlg-backdrop"></div>');
      const foot =
        (cancelLabel !== null ? '<button type="button" class="ed-dlg-btn cancel"></button>' : '') +
        (!hideOk ? '<button type="button" class="ed-dlg-btn ok"></button>' : '');
      const dlg = createElement(
        '<div class="ed-dlg" role="dialog" aria-modal="true">' +
          '<div class="ed-dlg-head"><div class="ed-dlg-title"></div></div>' +
          '<div class="ed-dlg-body"><div class="ed-dlg-msg"></div></div>' +
          '<div class="ed-dlg-foot">' + foot + '</div>' +
        '</div>'
      );

      dlg.querySelector('.ed-dlg-title').textContent = title;
      dlg.querySelector('.ed-dlg-msg').textContent = message;

      const bCancel = dlg.querySelector('button.cancel');
      const bOk = dlg.querySelector('button.ok');
      if (bCancel) bCancel.textContent = cancelLabel;
      if (bOk) {
        bOk.textContent = okLabel;
        if (dangerOk) bOk.classList.add('danger');
      }

      function cleanup(confirmed) {
        document.removeEventListener('keydown', handleKey);
        backdrop.remove();
        resolve(hideOk ? undefined : confirmed);
      }
      function handleKey(e) {
        if (e.key === 'Escape') { cleanup(false); return; }
        if (e.key === 'Enter')  { cleanup(true); }
      }

      backdrop.addEventListener('mousedown', function (e) {
        if (e.target === backdrop) cleanup(false);
      });
      if (bCancel) bCancel.addEventListener('click', () => cleanup(false));
      if (bOk)     bOk.addEventListener('click',     () => cleanup(true));

      backdrop.appendChild(dlg);
      document.body.appendChild(backdrop);
      document.addEventListener('keydown', handleKey);

      // Focus OK by default so Enter/Space fires it; falls back to
      // Cancel when OK is hidden (alert-style).
      setTimeout(function () { (bOk || bCancel).focus(); }, 0);
    });
  }

  // alert(title, message) → Promise<void>. One-button card; the message
  // body respects \n so multi-line error reports read cleanly without
  // HTML escaping hoops in the caller. `esc` kept imported so a future
  // caller that wants HTML can branch.
  dialog.alert = function (title, message) {
    return dialog({ title: title || '', message: message || '', cancel: false });
  };

  // confirm(message, title) OR confirm({title, message, okText, cancelText, dangerOk})
  // → Promise<boolean>. Matches the goop2 flavour.
  dialog.confirm = function (messageOrOpts, title) {
    if (typeof messageOrOpts === 'object' && messageOrOpts !== null) {
      const o = messageOrOpts;
      return dialog({
        title:       o.title       || 'Confirm',
        message:     o.message     || '',
        okText:      o.okText      || 'OK',
        cancelText:  o.cancelText  || 'Cancel',
        dangerOk:    !!o.dangerOk,
      });
    }
    return dialog({ title: title || 'Confirm', message: String(messageOrOpts || '') });
  };

  // prompt({ title, message, defaultValue, placeholder, okText,
  // cancelText, password }) → Promise<string|null>. Resolves with
  // null on Cancel / Escape / backdrop click; the input's current
  // value (not trimmed) on OK / Enter. The input is focused on
  // open and its content is pre-selected so a teammate can retype
  // without reaching for the keyboard.
  dialog.prompt = function (opts) {
    opts = opts || {};
    const title = opts.title || '';
    const message = opts.message || '';
    const okLabel = opts.okText || 'OK';
    const cancelLabel = opts.cancelText || 'Cancel';
    const placeholder = opts.placeholder || '';
    const defaultValue = opts.defaultValue != null ? String(opts.defaultValue) : '';
    const inputType = opts.password ? 'password' : 'text';

    return new Promise(function (resolve) {
      const backdrop = createElement('<div class="ed-dlg-backdrop"></div>');
      const dlg = createElement(
        '<div class="ed-dlg" role="dialog" aria-modal="true">' +
          '<div class="ed-dlg-head"><div class="ed-dlg-title"></div></div>' +
          '<div class="ed-dlg-body">' +
            '<div class="ed-dlg-msg"></div>' +
            '<input class="ed-dlg-input" type="' + inputType + '" autocomplete="off">' +
          '</div>' +
          '<div class="ed-dlg-foot">' +
            '<button type="button" class="ed-dlg-btn cancel"></button>' +
            '<button type="button" class="ed-dlg-btn ok"></button>' +
          '</div>' +
        '</div>'
      );

      dlg.querySelector('.ed-dlg-title').textContent = title;
      dlg.querySelector('.ed-dlg-msg').textContent = message;

      const input = dlg.querySelector('.ed-dlg-input');
      input.value = defaultValue;
      if (placeholder) input.setAttribute('placeholder', placeholder);

      const bCancel = dlg.querySelector('button.cancel');
      const bOk     = dlg.querySelector('button.ok');
      bCancel.textContent = cancelLabel;
      bOk.textContent     = okLabel;

      function cleanup(value) {
        document.removeEventListener('keydown', handleKey);
        backdrop.remove();
        resolve(value);
      }
      function handleKey(e) {
        if (e.key === 'Escape') { cleanup(null); return; }
        if (e.key === 'Enter')  { cleanup(input.value); }
      }

      backdrop.addEventListener('mousedown', function (e) {
        if (e.target === backdrop) cleanup(null);
      });
      bCancel.addEventListener('click', () => cleanup(null));
      bOk.addEventListener('click',     () => cleanup(input.value));

      backdrop.appendChild(dlg);
      document.body.appendChild(backdrop);
      document.addEventListener('keydown', handleKey);

      // Focus the input (not a button) so typing starts immediately.
      // Select the default value — the common case is editing an
      // existing name, so the admin types a replacement rather than
      // appending.
      setTimeout(function () { input.focus(); input.select(); }, 0);
    });
  };

  // keep `esc` referenced so bundlers/linters don't flag it as dead;
  // a future caller that wants inline HTML can plug it in here.
  dialog._esc = esc;

  window.GG = window.GG || {};
  window.GG.dialog = dialog;
})();
