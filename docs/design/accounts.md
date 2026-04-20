# Accounts (admin + regular)

Status: **proposal**, Phase 1 not yet started (2026-04-20). Supersedes
the README roadmap item "NaCl-challenge admin login" — which is
retired, see §1. This document is the source of truth for how humans
identify themselves to GiGot. Implementation will live in
`internal/accounts/` (single sealed store, evolved from the current
`internal/admins/`) and a thin principal check at the login handler.

---

## 1. Why not "NaCl-challenge admin login"

The README proposed replacing password+session with curve25519
challenge/response, private key in browser `localStorage`. The hole:
`localStorage` is wiped by clearing browsing data, private windows,
browser reinstalls, and doesn't follow the admin to a new device — so
admins lock themselves out in normal usage. And keeping password "as a
fallback" leaves the password attack surface intact, so the NaCl path
added complexity without removing anything. Retired. The real question
is not "what crypto proves identity" but "who is known to this
server" — which is an account model, below.

---

## 2. The model: one noun, one role field

GiGot has one kind of human principal:

```go
type Account struct {
    Provider     string  // local | github | microsoft | gateway
    Identifier   string  // lowercased: username, github login, email, ...
    Role         string  // admin | regular     (closed set)
    DisplayName  string  // optional, for UI
    PasswordHash string  // bcrypt; only populated for provider=local
    CreatedAt    time.Time
}
```

Keyed by `(Provider, Identifier)`. `Role` is a closed set of two:

- **admin** — can log into `/admin`, manage accounts, issue subscription
  tokens, edit credentials / destinations.
- **regular** — a registered human who does *not* administer the server.
  Purpose: subscriptions get issued *to* a regular account (the token's
  `username` becomes a reference to an Account, not free text).

No third role. Every future capability is either "admin does it" or
"regular does it," not a finer matrix.

### Providers

- **local** — username + bcrypt password, held in `accounts.enc`.
  Identifier is the chosen username (lowercased).
- **github** — OAuth login verified against GitHub. Identifier is the
  GitHub login (`login` field; lowercased, case-insensitive on GitHub's
  side anyway).
- **entra** — OIDC login against a configured Microsoft Entra ID
  tenant (work/school accounts). Identifier is the `oid` claim — a
  stable GUID, tenant-scoped. This is the path enterprise deploys will
  use.
- **microsoft** — OIDC login against consumer Microsoft Accounts
  (outlook.com, hotmail.com, live.com — the `consumers` audience).
  Identifier is the `sub` claim. Kept separate from `entra` on purpose:
  the trust boundary differs (any MSA vs. a specific tenant) and the
  identifier shape differs, so mixing them in one row would be a
  footgun. Most deploys will only turn on one of the two.
- **gateway** — identity forwarded by a signed header from a trusted
  fronting proxy (APIM etc.), verified on ingress.

Providers are orthogonal to role: a `local` admin, a `github` regular,
an `entra` admin are all legal combinations.

---

## 3. Storage: one sealed store

Single file: `data/accounts.enc`, sealed to the server's own NaCl
public key. Same pattern as `clients.enc`, `tokens.enc`, etc.

**Why one store, not two.** Splitting identity from secret sounds tidy
but earns nothing: both are sensitive enough to seal, access patterns
are identical, and two stores means two migration paths.
`PasswordHash` is empty on non-local rows — a dormant field, not a
layering violation.

**Migration from the existing `admins.enc`.** On the first boot of the
new version:
1. If `accounts.enc` exists, use it.
2. Else if `admins.enc` exists, read each row, map to
   `Account{Provider: "local", Identifier: <username>, Role: "admin",
   PasswordHash: <existing>, CreatedAt: <existing>}`, write
   `accounts.enc`, leave `admins.enc` in place as a backup for one
   release, emit a startup log line.
3. Else start empty; the config-seeded admins (§4) populate on first
   successful load.

---

## 4. Config

Two new config surfaces:

```json
{
  "auth": {
    "allow_local": true
  },
  "admins": [
    { "provider": "local",  "identifier": "admin",                                    "display_name": "Primary admin" },
    { "provider": "github", "identifier": "peter-vdpas",                              "display_name": "Peter (GH)"    },
    { "provider": "entra",  "identifier": "11111111-2222-3333-4444-555555555555",     "display_name": "Peter (work)"  }
  ]
}
```

### `auth.allow_local` (bool)

- `true`: local password login is accepted. Local-provider accounts can
  be created and used.
- `false`: local path is disabled at the router — `/api/admin/login`
  returns 404. Only non-local providers (gateway, OAuth) can
  authenticate.
- **Default `true`** while Phases 2–3 are unshipped — flipping to
  `false` today would leave no way in.
- **CLI override**: `gigot serve --allow-local=true|false` beats the
  config value for the invocation. Useful for break-glass ("the OAuth
  IdP is down, let me in with the local password for ten minutes").

### `admins` (array) — bootstrap seed only

This is **not** the admin allowlist. It's a seed list: on startup, any
entry not already present in the account store is upserted with
`role=admin`. After bootstrap, the account store is the source of
truth — removing an entry from config does *not* demote or remove the
account. This matters because Phase 2's registration flow writes to the
account store, and we can't let the config file overwrite live data
every restart.

Default ships with one entry: `{provider: "local", identifier: "admin",
display_name: "Primary admin"}`. First-run UX unchanged.

---

## 5. Login flow (Phase 1, local only)

`POST /api/admin/login {username, password}`:

1. If `!auth.allow_local` → 404 (endpoint doesn't exist in this mode).
2. Load account by `(provider: "local", identifier: username)`. Not
   found → 401 `invalid credentials`.
3. `bcrypt.CompareHashAndPassword(account.PasswordHash, password)` —
   fail → 401 `invalid credentials`.
4. Require `account.Role == "admin"`. Otherwise → 401 `invalid
   credentials` (opaque to the caller, logged as
   `login: account role=regular, /admin denied: <identifier>`
   server-side).
5. Issue session cookie, return `{identifier, display_name, role}`.

The `invalid credentials` response is identical across misses 2, 3,
and 4. We don't tell attackers which gate they failed.

---

## 6. Subscription tokens bind to accounts

Today: `POST /api/admin/tokens` takes a free-text `username` that
doesn't have to correspond to anyone real. New rule: the `username` on
a newly-issued token **must** match an existing `Account`'s
`(provider, identifier)`. Accepted shorthand: a bare string means
`(provider: local, identifier: string)`; full form is an object
`{provider, identifier}`.

- Rejection at issuance if no matching account exists.
- **Back-compat**: tokens issued before Phase 1 keep working. They're
  flagged in the admin UI as "legacy — no account binding" with an
  action to bind to an existing account. No forced migration in Phase 1.

This is the load-bearing change: subscription tokens stop being
disembodied bearers and start pointing at a real person.

---

## 7. Registration (Phase 2)

`/register` endpoint + page. While `allow_local` is on, anyone can
register a local account with `role=regular`. Admins can optionally
promote regulars via `/api/admin/accounts/{id}/role`.

Admin-only "invite" flow is out of scope for Phase 2 — a real deploy
that wants invites-only can turn off `/register` at the router and use
admin-driven account creation. Design that path when someone actually
needs it.

---

## 8. OAuth / OIDC (Phase 3)

Add redirect-flow login endpoints — one per enabled provider — driven
by the standard OAuth 2.0 authorization-code flow. Each enabled
provider contributes `/admin/login/<provider>` and
`/admin/login/<provider>/callback`:

- `/admin/login/github` — uses GitHub's OAuth. Identifier = `login`.
- `/admin/login/entra` — uses a configured Entra ID tenant's OIDC
  discovery URL (`https://login.microsoftonline.com/<tenant>/v2.0`).
  Identifier = `oid`.
- `/admin/login/microsoft` — uses the `consumers` audience
  (`https://login.microsoftonline.com/consumers/v2.0`). Identifier =
  `sub`.

Each callback verifies the token, extracts the identifier claim,
resolves the corresponding `Account` by `(provider, identifier)`. If
the account doesn't exist yet, either (a) auto-register with
`role=regular` and show a landing page, or (b) reject with "ask an
admin to register you." A per-provider config flag selects.

### Library choice: plain OIDC, not MSAL

- `github.com/coreos/go-oidc/v3/oidc` — discovery + ID-token
  verification. Works for Entra, consumer Microsoft, Google, Okta, any
  OIDC provider. Used for `entra` and `microsoft`.
- `golang.org/x/oauth2` — authorization-code exchange, state + PKCE
  handling, token refresh. Used by all three providers.
- `golang.org/x/oauth2/github` — the one github-specific shim
  (endpoint URLs + scope names). Tiny.

**Deliberately not pulling in MSAL** (Microsoft's auth SDK). MSAL earns
its weight when a client needs device-code flow, conditional-access
claims challenges, broker-based SSO on Windows, or cross-process token
cache. A browser-redirect admin login needs none of that. Standard OIDC
is fewer dependencies, fewer Microsoft-specific abstractions, and the
same code handles GitHub — one flow, parameterised by discovery URL
and client ID, per provider.

### Config shape (Phase 3)

```json
{
  "auth": {
    "allow_local": true,
    "oauth": {
      "github":    { "enabled": true, "client_id": "...", "client_secret_ref": "github-oauth" },
      "entra":     { "enabled": true, "tenant_id": "contoso.onmicrosoft.com", "client_id": "...", "client_secret_ref": "entra-oauth" },
      "microsoft": { "enabled": false }
    }
  }
}
```

`client_secret_ref` names a credential in the existing credential vault
(`internal/credentials/`) rather than embedding the secret in the
config file — vault already does sealed-at-rest storage, so the OAuth
secrets ride along for free.

Once Phase 3 lands, the recommended default of `allow_local` flips to
`false` *in docs*. The actual default in `Defaults()` stays `true`
until at least one more release cycle, because flipping defaults in a
minor version silently locks users out.

---

## 9. Gateway-trusted identity (Phase 4)

New `auth.GatewayStrategy` reads a configured signed-header claim
(e.g. `X-MS-CLIENT-PRINCIPAL-NAME` from APIM) → resolves to
`(provider: "gateway", identifier: <claim>)`. Same account-store check
as everywhere else.

---

## 10. Phasing summary

| Phase | What lands                                                                 |
|-------|----------------------------------------------------------------------------|
| 1     | `internal/accounts/` store (migrate from `admins.enc`), Role field, config `auth.allow_local` + CLI flag, `admins` seed, login handler role-gate, subscription issuance binds to account. |
| 2     | `/register` + regular account creation, admin accounts-list UI, role-change, legacy-token "bind to account" action.                                     |
| 3     | OAuth / OIDC (GitHub, Entra, consumer Microsoft) for both login and registration, via `go-oidc` + `oauth2` — **no MSAL**. NaCl-challenge roadmap item formally retired in README. |
| 4     | Gateway-trusted identity strategy (aligns with Roadmap #2).                                                                                             |
| 5     | Flip documented default `allow_local` → `false`; optionally remove the local password path entirely if no deploy depends on it.                        |

---

## 11. Non-goals

- **More roles.** No `viewer`, no `operator`, no per-repo roles. If a
  deploy needs finer-grained control, subscription tokens already carry
  per-repo allowlists and abilities — use those.
- **Group / tenant membership** ("anyone in Azure AD group X"). Every
  IdP expresses groups differently. Phase 3 stays on explicit
  individual accounts.
- **Passkeys / WebAuthn.** A possible future alternative to the `local`
  password path, orthogonal to everything above. Not on the critical
  path.
