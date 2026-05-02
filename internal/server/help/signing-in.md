# Signing in

Most GiGot users never sign in to the website at all, Formidable
talks to the server on their behalf using a **subscription key** the
operator hands them. That key is yours alone — the operator issues
one per user per repo, never one shared key for a whole team — so
treat it like a password: don't paste it into chat, don't share it
with a teammate, and ask for a new one if you suspect it leaked.

The two reasons to actually sign in are:

- You're an admin (or maintainer) and need to manage repos, keys, or
  accounts via the [admin console](/admin/repositories).
- You're a regular user who wants to see your own subscription keys
  on the [/user](/user) page — useful when re-installing Formidable
  on a new machine.

## Sign-in methods

GiGot supports three sign-in paths, configured per server. The login
card at [/signin](/signin) shows whichever ones the operator has
enabled.

### 1. Local username + password

The default. Visit [/signin](/signin), enter your credentials, click
**Sign in**. If your operator allows self-service registration
(`auth.allow_local: true`), you can create an account at
[/admin/register](/admin/register); otherwise an admin has to create
it for you.

### 2. OAuth / OIDC

When an identity provider is configured (GitHub, Microsoft Entra),
the login card shows a **Sign in with &lt;provider&gt;** button next
to the username field. Click it, complete the provider's flow, and
you're returned to GiGot already signed in. Your role on first sign
in is `regular`; an admin can promote you afterwards on the
[Accounts](/admin/accounts) page.

### 3. Gateway-trusted header

When GiGot sits behind an authenticating reverse proxy (Azure API
Management, Cloudflare Access, an internal SSO gateway), the proxy
injects a signed user header and you skip the sign-in card entirely
— if your proxy session is valid, GiGot already knows who you are.

## Forgot your password?

There is no self-service password reset. Ask an admin to set a new
one for you on the [Accounts](/admin/accounts) page, or have the
operator run `gigot -add-admin <username>` on the host (the same
flag works for resets, despite the name — it prompts for a new
password and updates the existing account in place).

## Signing out

Use the kebab menu next to your name in the admin sidebar, or hit
[/admin/logout](/admin/logout) directly. OAuth sessions sign out of
GiGot only; your identity provider session stays open.
