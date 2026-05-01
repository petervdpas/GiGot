# Accounts (admin + maintainer + regular)

Status: Phases 1–5 all shipped 2026-04-20 (Phase 5 is a doc-only
default-flip; see §10). Supersedes the README roadmap item
"NaCl-challenge admin login" — which is now formally retired (see §1).
This document is the source of truth for how humans identify
themselves to GiGot. The implementation lives in
`internal/accounts/` (sealed store, migrated from the former
`internal/admins/`), `internal/auth/oauth/` (Phase-3 redirect-flow
providers), plus thin principal checks in the login handlers.

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

## 2. The model: one noun, three role tiers

GiGot has one kind of human principal:

```go
type Account struct {
    Provider     string  // local | github | microsoft | gateway
    Identifier   string  // lowercased: per-provider stable handle (see §2.6.1)
    Role         string  // admin | maintainer | regular   (closed set)
    DisplayName  string  // optional, for UI
    Email        string  // user-facing handle, lowercased; populated by IdP
    PasswordHash string  // bcrypt; only populated for provider=local
    CreatedAt    time.Time
}
```

`Email` is independent of `Identifier`. For some providers
(github, microsoft consumer) Identifier IS the email; for others
(entra → `oid`, local → username) it isn't. Email is the
human-recognisable handle every UI surface ("signed in as", account
chips, audit pills) reads — keeping it as a separate field means a
stable user-facing label even when the identifier is something
machine-shaped. OAuth callbacks populate Email from the IdP's
`email` claim on every login, refreshing the stored value when it
changes upstream; local accounts get it set explicitly via the
admin form or the registration page.

Keyed by `(Provider, Identifier)`. `Role` is a closed set of three:

- **admin** — can log into `/admin`, manage accounts, create/delete
  repos, issue any subscription key, write credentials, configure auth.
  The full server-administration tier.
- **maintainer** — between admin and regular. Can hold subscription keys
  carrying the `mirror` ability, manage their own repos' mirror
  destinations via the subscriber API, fire manual syncs, and read
  credential **names** (not secrets) so they can reference vault entries
  when wiring mirrors. Cannot create accounts, repos, other people's
  keys, or write secrets to the vault.
- **regular** — Formidable end user. Holds subscription keys for
  push/pull of templates and records, nothing else. Cannot hold the
  `mirror` ability — the role is a structural fence on top of the
  per-token ability bits, so granting `mirror` to a regular's key is
  rejected at issue time and at request time.

The role is the **capability tier** an account is allowed to operate in.
Per-token granularity (which repo, which abilities within the tier)
still lives on subscription tokens. Concretely: the `mirror` ability is
admin/maintainer-only at the role layer; admins decide *which* of those
keys actually carry it via the abilities allowlist. Two layers, both
required for any mirror-related call to succeed.

No fourth role. No per-repo roles. If a future capability needs a
finer fence than the role layer, it gets a new ability bit (gated by
the appropriate role), not a new role tier.

### Providers

Per-provider identifier choice and what populates `Account.Email`:

- **local** — username + bcrypt password, held in `accounts.enc`.
  Identifier is the chosen username (lowercased). Email is set
  explicitly via the admin POST/PATCH form or the `/register` page
  — there's no IdP to pull it from.
- **github** — OAuth verified against GitHub. Identifier is the
  user's **primary verified email** (lowercased), fetched from
  `/user/emails` with the `user:email` scope. Login (`login` field)
  was the identifier prior to 2026-05-01; it's now only used as a
  fallback display name if the IdP omits `name`. Same email +
  Identifier means there's just one row.
- **entra** — OIDC against a configured Microsoft Entra ID tenant
  (work/school accounts). Identifier is the `oid` claim — a stable
  GUID, tenant-scoped, doesn't rotate when the user's email changes.
  Email comes from the `email` claim in the same ID token,
  populated alongside; the two are stored independently so an
  email change on the work side doesn't re-key the row. This is the
  path enterprise deploys will use.
- **microsoft** — OIDC against consumer Microsoft Accounts
  (outlook.com, hotmail.com, live.com — the `consumers` audience).
  Identifier is the **`email` claim** (lowercased). The `sub` claim
  was the identifier prior to 2026-05-01; per spec it's unique
  per (client_id, user) — same human across two App Registrations
  got two distinct subs, which created duplicate rows in
  multi-environment deploys. Email is stable across App
  Registrations, so it's the right key. Kept separate from `entra`
  on purpose: the trust boundary differs (any MSA vs. a specific
  tenant), and the identifier shape no longer matches between them
  either.
- **gateway** — identity forwarded by a signed header from a
  trusted fronting proxy (APIM etc.), verified on ingress.
  Identifier is whatever the upstream claim says; Email is
  populated only if the gateway forwards it as a separate header
  (out of scope for the current Phase 4 implementation).

Providers are orthogonal to role: a `local` admin, a `github` regular,
a `github` maintainer, an `entra` admin are all legal combinations.

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
- **Runtime default `true`** — flipping the ship-level default to
  `false` in a minor version would silently lock any deploy without
  OAuth/gateway out of their own server; Phase 5's guidance is to set
  `allow_local: false` explicitly in `gigot.json` once a non-local
  path is wired up, and the runtime will warn at boot when the flag
  is off but no non-local path can actually admit an admin.
- **Recommended in deploys** (Phase 5): set `allow_local: false`
  explicitly once OAuth or gateway is configured and at least one
  non-local admin exists. Keep the break-glass flag (`--allow-local`)
  for emergencies.
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

Today: `POST /api/admin/tokens` requires a `username` that binds to
an existing `Account`. Two shapes are accepted:

- **Scoped** `"provider:identifier"` (e.g. `"github:petervdpas"`,
  `"entra:<oid>"`, `"local:alice"`). Preferred form; matches the
  `(provider, identifier)` key in the accounts store exactly.
- **Bare** `"identifier"` — shorthand for `"local:identifier"`, kept
  for back-compat with callers written before the accounts model
  (integration tests, Postman collection, CLI demos). Any known
  provider prefix is always interpreted as scoped, so `"github:x"`
  never falls back to local even if the GitHub account is missing —
  it 400s, correctly.

Shipped 2026-04-20. Supersedes the earlier design note saying the
scoped form was future work.

**Phase 1 was permissive** (shipped, now retired): if no account
existed for the bare username, the handler auto-created one with
`role=regular` and logged the event. Kept integration tests and the
Postman collection working during the transition without a manual
"register this username first" ritual.

**Phase 2 tightens this** (shipped 2026-04-20): the permissive
auto-create is gone. Issuing a token for an unknown username returns
`400` with a message pointing at `/register` or
`POST /api/admin/accounts`. Deliberate step, not silent drift.

**Back-compat for existing tokens.** Tokens issued before Phase 1 keep
working. `GET /api/admin/tokens` reports `has_account: false` on
rows whose `username` has no matching account, and the admin UI shows
a "legacy — no account" badge plus a **Bind to account** button. The
bind action (`POST /api/admin/tokens/bind`) creates the missing
role=regular account so no token is left dangling. No forced
migration — admins bind on their own schedule.

This is the load-bearing change: subscription tokens stop being
disembodied bearers and start pointing at a real account.

### 6.1 Role-vs-ability fences

Token abilities (e.g. `mirror`) are gated twice:

1. **Issue time** — `POST /api/auth/token` and
   `PATCH /api/admin/tokens` reject any abilities the issued account's
   role is not entitled to hold. Today: `mirror` requires admin or
   maintainer; granting it to a regular returns 400. Keeps stored state
   honest, gives admins a clear error instead of a silently-dead bit.
2. **Request time** — every mirror-related endpoint
   (`/api/repos/{name}/destinations*`) calls
   `requireMaintainerOrAdmin` *on top of* the existing
   `TokenAbilityPolicy("mirror")` check. A stale `mirror` bit on a
   key issued before the role fence existed (or on an account that was
   later demoted) still fails at request time. No migration of old
   tokens needed — they go inert without storage churn.

Two layers, both required. Removing either leaves a hole: dropping
the runtime check means demotions take effect only on next key
re-issue; dropping the issue-time check means the admin UI silently
accumulates dead bits.

### 6.2 Credentials stay GiGot-side

Mirror credentials and destinations are GiGot's domain end-to-end.
The admin Repositories page configures destinations *and* picks the
credential to use, on a privacy-notice consent gesture per
remote-sync.md §3.7. Maintainer-role subscribers can *trigger* a
configured destination's push (`POST /destinations/{id}/sync`) —
that's the legitimate Formidable-side action — but credential names
never cross the GiGot↔client boundary, and Formidable holds no
mirror-config UI. Sealed-body trust + single-source-of-truth: GiGot
owns destinations, the client only fires them.

---

## 7. Registration (Phase 2, shipped 2026-04-20)

`POST /api/register` + `/admin/register` page. While `allow_local` is
on, anyone can register a local account with `role=regular` (409 on
duplicate, 404 when `allow_local` is false). Admins can promote,
demote, reset passwords, and delete accounts via the `accounts`
console at `/admin/accounts`, backed by:

- `GET /api/admin/accounts` — list every known account.
- `POST /api/admin/accounts` — admin-driven create (any provider, any
  role, optional password on local accounts).
- `PATCH /api/admin/accounts/{provider}/{identifier}` — update role,
  display name, and/or local password.
- `DELETE /api/admin/accounts/{provider}/{identifier}` — remove. The
  server refuses to demote or delete the last `admin` so the console
  can't lock itself out (409).

Admin-only "invite" flow is out of scope — a deploy that wants
invites-only can turn off `/api/register` at a reverse proxy and rely
on admin-driven account creation. Design that path when someone
actually needs it.

---

## 8. OAuth / OIDC (Phase 3, shipped 2026-04-20)

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

## 9. Gateway-trusted identity (Phase 4, shipped 2026-04-20)

`internal/auth/gateway/` holds a small `Verifier` that validates
three headers per request: a claimed identifier, a Unix timestamp,
and a hex HMAC-SHA256 signature over `"<identifier>\n<timestamp>"`
keyed on a shared secret. Server-side bridge
(`internal/server/handler_gateway.go`) wraps the Verifier as an
`auth.Strategy`, resolves the claim against the accounts store, and
returns an `Identity` with `Provider: "gateway"`. Registered after
the session strategy so a bearer token or session cookie still wins
when present.

### Admin path

`requireAdminSession` accepts either the session cookie OR a valid
gateway triple whose identifier resolves to a `role=admin` account.
Role is re-checked per request so a demote takes effect immediately
without waiting for any cookie to expire. Regular gateway accounts
fall through to 401 on admin routes — same behaviour as a bearer
token hitting an admin route.

### Why HMAC + timestamp, not "trust the proxy IP"

IP-allowlists collapse the moment the proxy is moved behind another
load balancer or the operator skips a config knob. A shared HMAC
secret is simpler to reason about, portable across hosting shapes
(APIM, nginx, oauth2-proxy, Envoy, a plain nginx `auth_request`
lane), and tamper-evident: a man-in-the-middle on the proxy→GiGot
link can't flip the identifier without invalidating the signature.
The timestamp + `max_skew_seconds` window (default 5 minutes) stops
trivial replay; a caller who captures a valid header can only reuse
it briefly, and GiGot can log the event. The proxy is still trusted
end-to-end for authenticating the user — the HMAC only proves the
forwarded claim wasn't rewritten in flight.

### Config shape (Phase 4)

```json
{
  "auth": {
    "gateway": {
      "enabled": true,
      "user_header": "X-GiGot-Gateway-User",
      "sig_header": "X-GiGot-Gateway-Sig",
      "timestamp_header": "X-GiGot-Gateway-Ts",
      "secret_ref": "gateway-hmac",
      "max_skew_seconds": 300,
      "allow_register": false
    }
  }
}
```

`secret_ref` names a credential in the existing credential vault —
same pattern as Phase-3 OAuth `client_secret_ref`. Header names are
configurable so deploys with an existing proxy convention (e.g.
`X-MS-CLIENT-PRINCIPAL-NAME`) can remap them instead of renaming
proxy configuration, though the shared contract — identifier,
timestamp, and signature-over-the-pair — is fixed. Any fronting
proxy can be configured to emit these headers; a Go-written proxy
can reuse `gateway.Sign` directly.

`allow_register=false` is the recommended default: admins stay an
explicit list. `allow_register=true` auto-creates a `role=regular`
account on first successful claim for deploys that want any
employee the gateway admits to be a known user.

### Non-goals

- **No group claim parsing.** The gateway forwards one identifier,
  not a membership list. Admin status lives on the account in the
  store, not on the claim.
- **No per-request mutation of role.** A demoted user loses access
  on the next request; a promoted user gains it on the next request;
  neither requires the proxy to know about GiGot's accounts state.

---

## 9.5 Hot-swap admin surface

`/admin/auth` (UI) + `GET`/`PATCH /api/admin/auth` (API) let an
authenticated admin inspect and rewrite the auth runtime without a
process restart. Landed alongside Phase 4.

### API contract

- `GET /api/admin/auth` → `AuthRuntimeView { allow_local, oauth: {
  github, entra, microsoft }, gateway, config_path }`. All fields
  are names, not secrets — `client_secret_ref` and `secret_ref` are
  vault lookup keys. The vault contents themselves never appear.
- `PATCH /api/admin/auth` with `AuthReloadRequest { allow_local,
  oauth, gateway }` → full-replace of those three sub-blocks.
  Partial-merge semantics are intentionally rejected (JSON PATCH's
  absent-vs-zero ambiguity is a documented footgun); the UI
  re-posts the complete snapshot every save.

### Atomicity

`Server.ReloadAuth` builds the candidate OAuth registry and gateway
strategy OUTSIDE the lock (discovery RPCs + secret lookups shouldn't
block every in-flight auth check), then takes `authMu.Lock` for the
pointer swaps + `cfg.Auth` assignment + `cfg.Save`. If any build
step fails — unresolvable `secret_ref`, unreachable discovery URL,
empty `client_id` on an enabled provider — the whole operation
returns the error and nothing is touched. Partial application is
the ONE outcome the handler refuses: reasoning about "did the
reload apply the allow_local flip but not the gateway block" would
be a mess.

### Why mutate the live Registry instead of a pointer swap

`oauth.Registry.Replace(name, p)` / `Remove(name)` mutate the
existing map under the registry's own mutex, so one `*Registry`
pointer lives for the whole process. `auth.Provider.Replace(s)` /
`Remove(name)` do the same for the strategy list. Callers that
captured a reference before the mutation finish against their
snapshot — safe because `snapshotStrategies()` always returns a
copy. The alternative (atomic pointer swap on the whole Registry)
would force in-flight `/admin/login/<provider>/callback` handlers
to either race the swap or hold a long-lived reference that defeats
garbage collection.

### Persistence

`Config.Path` is set in `config.Load` (runtime-only; `json:"-"` so
a Save round-trip can't leak it back into the file). On successful
reload, `cfg.Save(cfg.Path)` writes the updated Auth block back.
Path is empty in ad-hoc / test servers — those still reload fine
in-memory, just don't survive a restart.

### What's NOT on this surface

- **`cfg.Auth.Enabled`**, **`cfg.Auth.Type`** — boot-time wiring
  (middleware on/off, strategy-type selector). Flipping these at
  runtime would invalidate every in-flight session; out of scope.
- **`cfg.Crypto`**, **`cfg.Storage`** — file-system concerns that
  need a restart to re-open the sealed stores cleanly.
- **`cfg.Admins` seed list** — it's a bootstrap seed, not a live
  allowlist (§4); editing it via PATCH would be confusing.
  Admins-in-the-store come from `/admin/accounts`.

---

## 10. Phasing summary

| Phase | Status                 | What landed / lands                                                                                                                                       |
|-------|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1     | **Shipped 2026-04-20** | `internal/accounts/` store (migrated from `admins.enc`), Role field, config `auth.allow_local` + CLI flag, `admins` seed, login handler role-gate, permissive auto-create on token issuance. |
| 2     | **Shipped 2026-04-20** | `/api/register` + `/admin/register` page, admin accounts UI + API (list/create/patch/delete, last-admin protection), token issuance tightened to reject unknown usernames, legacy-token bind action. |
| 3     | **Shipped 2026-04-20** | OAuth / OIDC for GitHub (OAuth2 + /user API), Entra (OIDC, tenant-scoped, `oid` claim), and consumer Microsoft (OIDC, `consumers` audience, `sub` claim) via `go-oidc` + `oauth2` — **no MSAL**. Per-provider `allow_register` flag auto-creates `role=regular` accounts on first successful callback. `client_secret_ref` resolves against the existing credential vault. Scoped `"provider:identifier"` token binding (§6) lands in the same phase so OAuth accounts can actually hold subscription keys; `/admin/accounts` gains a `subscription_count` column with click-through to `/admin/subscriptions?user=<scoped>`. NaCl-challenge roadmap item formally retired in README. |
| 4     | **Shipped 2026-04-20** | `internal/auth/gateway/` HMAC-SHA256 signed-header strategy with configurable header names and `max_skew_seconds` replay window, `secret_ref` → credential vault lookup, `allow_register` flag. Registered on `auth.Provider` after session so cookies still win; `requireAdminSession` honours a gateway claim when it resolves to a `role=admin` account. Boot-time lockout-risk warning flags `allow_local=false` + no non-local path or no non-local admin. Aligns with Roadmap #2. |
| 5     | **Shipped 2026-04-20** | Documentation default flipped to `allow_local=false` for any deploy that has configured OAuth or gateway. Runtime `Defaults()` still ships `allow_local=true` — flipping that in a minor version would silently lock upgraders out; that mechanical flip is held for a separate release cycle with migration notes. |
| Hot-swap | **Shipped 2026-04-21** | `/admin/auth` UI + `GET`/`PATCH /api/admin/auth`. `Server.ReloadAuth` rebuilds OAuth registry + gateway strategy outside the lock, swaps atomically on success, persists to `gigot.json`. Old state is preserved on any build failure. Covered §9.5. |
| 6     | **Shipped 2026-04-30 — extended 2026-05-01** | Three-tier role model: added `maintainer` between `admin` and `regular` (§2). Ability fences moved to two layers: issue-time check (rejects `mirror` on regular accounts) + request-time `requireMaintainerOrAdmin` gate on `/api/repos/{name}/destinations*` so stale bits go inert without migration (§6.1). Subscription admin UI drops the chicken-and-egg `destination_count > 0` gate; the role IS the gate. Accounts admin UI gains a maintainer option in the role dropdown and a teal `.badge.maintainer` style. Credentials stay GiGot-side — destination CRUD remains the admin Repositories page's job (§6.2); the maintainer-role subscriber surface is push-trigger only. **2026-05-01 follow-up:** `Account.Email` first-class field (§2). GitHub identifier flipped from `login` → primary verified email (fetched via `/user/emails` with `user:email` scope). Microsoft consumer identifier flipped from `sub` → `email` (fixes the per-App-Registration duplicate-account problem; entra stays on `oid`). OAuth callback writes Email + DisplayName on auto-register and refreshes them on every login (preserves Role + PasswordHash + CreatedAt). `/api/me`, `AccountView`, `CreateAccountRequest`, `UpdateAccountRequest`, `RegisterRequest` all carry `email`. Subscription chips on the Repositories page and token-card titles render `display_name` plus a muted email suffix so two accounts that share a display name are distinguishable at a glance. |

---

## 11. Non-goals

- **More roles.** Three is the cap: admin / maintainer / regular. No
  `viewer`, no `operator`, no per-repo roles. If a deploy needs
  finer-grained control, subscription tokens already carry per-repo
  allowlists and abilities — gate new capabilities by adding ability
  bits at the appropriate role tier, not new tiers.
- **Group / tenant membership** ("anyone in Azure AD group X"). Every
  IdP expresses groups differently. Phase 3 stays on explicit
  individual accounts.
- **Passkeys / WebAuthn.** A possible future alternative to the `local`
  password path, orthogonal to everything above. Not on the critical
  path.
