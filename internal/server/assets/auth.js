// /admin/auth — hot-swap the auth runtime. Loads once on boot, renders
// the current state, POSTs a PATCH on "Apply and reload". The server
// rebuilds the oauth registry + gateway strategy atomically; a failure
// keeps the old state live and we surface the error in place.

(() => {
  const { api, guardSession, initSidebar, escapeHtml } = window.Admin;

  const OAUTH_PROVIDERS = [
    { key: 'github',    label: 'GitHub',                      needsTenant: false },
    { key: 'entra',     label: 'Entra (work or school)',      needsTenant: true  },
    { key: 'microsoft', label: 'Microsoft (personal MSA)',    needsTenant: false },
  ];

  api.getAuth = async function () {
    const r = await fetch('/api/admin/auth', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('load auth state failed');
    return r.json();
  };
  api.patchAuth = async function (body) {
    const r = await fetch('/api/admin/auth', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.json()).error || 'reload failed');
    return r.json();
  };

  GG.core.onReady(async () => {
    const who = await guardSession();
    if (!who) return;
    initSidebar('auth', who);

    const state = await api.getAuth();
    document.getElementById('cfg-path').textContent = state.config_path || '(unset — changes not persisted)';

    // Allow-local toggle.
    document.getElementById('allow-local-host').innerHTML = GG.toggle_switch.html({
      id: 'allow-local',
      checked: !!state.allow_local,
      ariaLabel: 'Allow local login',
    });

    // OAuth providers — one .oauth-block per IdP.
    const oauthHost = document.getElementById('oauth-panels');
    oauthHost.innerHTML = OAUTH_PROVIDERS.map(p => renderOAuthBlock(p, state.oauth[p.key] || {})).join('');
    OAUTH_PROVIDERS.forEach(p => {
      document.getElementById('oauth-' + p.key + '-enabled-host').innerHTML = GG.toggle_switch.html({
        id: 'oauth-' + p.key + '-enabled',
        checked: !!(state.oauth[p.key] && state.oauth[p.key].enabled),
      });
      document.getElementById('oauth-' + p.key + '-register-host').innerHTML = GG.toggle_switch.html({
        id: 'oauth-' + p.key + '-register',
        checked: !!(state.oauth[p.key] && state.oauth[p.key].allow_register),
      });
    });

    // Gateway fields.
    const gw = state.gateway || {};
    document.getElementById('gw-enabled-host').innerHTML = GG.toggle_switch.html({
      id: 'gw-enabled', checked: !!gw.enabled,
    });
    document.getElementById('gw-register-host').innerHTML = GG.toggle_switch.html({
      id: 'gw-register', checked: !!gw.allow_register,
    });
    setInputValue('gw-user_header',      gw.user_header);
    setInputValue('gw-sig_header',       gw.sig_header);
    setInputValue('gw-timestamp_header', gw.timestamp_header);
    setInputValue('gw-secret_ref',       gw.secret_ref);
    setInputValue('gw-max_skew_seconds', gw.max_skew_seconds || 300);
    setInputValue('gw-display_name',     gw.display_name);

    document.getElementById('apply-btn').addEventListener('click', applyChanges);
  });

  function setInputValue(id, v) {
    const el = document.getElementById(id);
    if (el) el.value = v == null ? '' : v;
  }
  function getInputValue(id) {
    const el = document.getElementById(id);
    return el ? String(el.value || '').trim() : '';
  }

  function renderOAuthBlock(def, cfg) {
    const k = def.key;
    const tenantRow = def.needsTenant
      ? '<label>Tenant ID<input id="oauth-' + k + '-tenant_id" value="' + escapeHtml(cfg.tenant_id || '') + '" placeholder="contoso.onmicrosoft.com"></label>'
      : '';
    return (
      '<div class="oauth-block">' +
        '<h3>' + escapeHtml(def.label) + '</h3>' +
        '<div class="auth-row">' +
          '<span class="auth-row-label">Enabled</span>' +
          '<span id="oauth-' + k + '-enabled-host"></span>' +
        '</div>' +
        '<div class="auth-row">' +
          '<span class="auth-row-label">Auto-register unknown users as regular</span>' +
          '<span id="oauth-' + k + '-register-host"></span>' +
        '</div>' +
        '<div class="auth-grid">' +
          '<label>client_id<input id="oauth-' + k + '-client_id" value="' + escapeHtml(cfg.client_id || '') + '"></label>' +
          '<label>client_secret_ref<input id="oauth-' + k + '-client_secret_ref" value="' + escapeHtml(cfg.client_secret_ref || '') + '" placeholder="oauth-' + k + '"></label>' +
          tenantRow +
          '<label>Display name<input id="oauth-' + k + '-display_name" value="' + escapeHtml(cfg.display_name || '') + '"></label>' +
        '</div>' +
      '</div>'
    );
  }

  function collectOAuthBlock(key, needsTenant) {
    const block = {
      enabled:           GG.toggle_switch.val('oauth-' + key + '-enabled'),
      client_id:         getInputValue('oauth-' + key + '-client_id'),
      client_secret_ref: getInputValue('oauth-' + key + '-client_secret_ref'),
      allow_register:    GG.toggle_switch.val('oauth-' + key + '-register'),
      display_name:      getInputValue('oauth-' + key + '-display_name'),
    };
    if (needsTenant) block.tenant_id = getInputValue('oauth-' + key + '-tenant_id');
    return block;
  }

  async function applyChanges() {
    const msg = document.getElementById('reload-msg');
    msg.textContent = 'Applying…';
    msg.className = 'muted';
    const body = {
      allow_local: GG.toggle_switch.val('allow-local'),
      oauth: {
        github:    collectOAuthBlock('github', false),
        entra:     collectOAuthBlock('entra', true),
        microsoft: collectOAuthBlock('microsoft', false),
      },
      gateway: {
        enabled:          GG.toggle_switch.val('gw-enabled'),
        allow_register:   GG.toggle_switch.val('gw-register'),
        user_header:      getInputValue('gw-user_header'),
        sig_header:       getInputValue('gw-sig_header'),
        timestamp_header: getInputValue('gw-timestamp_header'),
        secret_ref:       getInputValue('gw-secret_ref'),
        max_skew_seconds: parseInt(getInputValue('gw-max_skew_seconds'), 10) || 300,
        display_name:     getInputValue('gw-display_name'),
      },
    };
    try {
      await api.patchAuth(body);
      msg.textContent = 'Applied. Old state replaced atomically.';
      msg.className = 'ok';
    } catch (err) {
      msg.textContent = 'Reload rejected: ' + err.message + ' — previous state still live.';
      msg.className = 'err';
    }
  }
})();
