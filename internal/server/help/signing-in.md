# Signing in

> *Stub — fill me in.*

GiGot supports three sign-in paths, configured per server:

1. **Local username + password** — the default. Visit [/signin](/signin)
   and enter your credentials.
2. **OAuth / OIDC** — when an identity provider is configured (GitHub,
   Microsoft Entra), the login card shows a button per provider.
3. **Gateway-trusted header** — when GiGot sits behind an authenticating
   proxy, the proxy injects a signed user header and you skip the
   sign-in card entirely.

## Forgot your password?

Local passwords can only be reset by an admin via the
[Accounts](/admin/accounts) page, or from the operator's CLI:
`gigot -admin-set-password`.

## Self-service registration

If the operator has enabled `auth.allow_local`, you can register a new
local account at [/admin/register](/admin/register).
