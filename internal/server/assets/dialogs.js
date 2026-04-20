// GG.dialog — backdrop-modal replacements for the native alert() and
// confirm() prompts. Ported from ~/Projects/goop2/internal/ui/assets/js/dialogs.js
// and pared down to the two surfaces GiGot needs today (alert, confirm) —
// no input matching, no file picker, no custom body builder. Add those
// back from the goop2 original if a future page needs them.
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

  // keep `esc` referenced so bundlers/linters don't flag it as dead;
  // a future caller that wants inline HTML can plug it in here.
  dialog._esc = esc;

  window.GG = window.GG || {};
  window.GG.dialog = dialog;
})();
