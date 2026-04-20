// /admin/register — self-service registration page. Public (no
// session needed). On success, the backend creates a role=regular
// local account and we redirect to the login page. See
// docs/design/accounts.md §7.

(function () {
  const form = document.getElementById('register-form');
  const err = document.getElementById('register-error');
  const ok = document.getElementById('register-ok');

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    err.textContent = '';
    ok.textContent = '';

    const body = {
      username: form.username.value.trim(),
      password: form.password.value,
    };
    const display = form.display_name.value.trim();
    if (display) body.display_name = display;

    try {
      const r = await fetch('/api/register', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
      });
      if (!r.ok) {
        const data = await r.json().catch(() => ({}));
        throw new Error(data.error || 'registration failed');
      }
      ok.textContent = 'Account created. Redirecting to sign-in…';
      // Short delay so the user actually sees the confirmation before
      // the page jumps; matches the timing of the admin-login redirect.
      setTimeout(() => { location.href = '/admin'; }, 700);
    } catch (ex) {
      err.textContent = ex.message;
    }
  });
})();
