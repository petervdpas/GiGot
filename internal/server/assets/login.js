// /admin login page. If a session already exists, bounce straight to
// /admin/repositories so the login card never flashes. Otherwise bind
// the login form and redirect on success.

(async function () {
  const { api } = window.Admin;

  // Already signed in → skip the form entirely. This is the same
  // behaviour as before: the old admin.js hid the login card and
  // showed the SPA when session() returned a user.
  try {
    const who = await api.session();
    if (who && who.username) {
      location.href = '/admin/repositories';
      return;
    }
  } catch { /* fall through to login form */ }

  const form = document.getElementById('login-form');
  const err = document.getElementById('login-error');
  form.addEventListener('submit', async e => {
    e.preventDefault();
    err.textContent = '';
    try {
      await api.login(form.username.value, form.password.value);
      location.href = '/admin/repositories';
    } catch (ex) {
      err.textContent = ex.message;
    }
  });
})();
