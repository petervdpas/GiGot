# GiGot

Git-backed server for [Formidable](https://github.com/petervdpas/Formidable). GiGot
gives Formidable clients an optional, server-centered place to clone, push, and pull
templates and context while keeping Formidable itself local-first.

On first connect, a client receives a full clone of all templates and context.
After that, everything works locally with incremental sync back to GiGot via the
standard Git smart-HTTP protocol.

GiGot is designed to run in two very different deployment modes:

1. **Standalone.** A single binary, optionally fronted by your own reverse proxy,
   using its built-in authentication, encryption, and admin UI.
2. **Behind an API gateway** such as Azure API Management. In this mode the
   gateway handles TLS termination, subscription-key enforcement, rate-limiting,
   and identity, while GiGot focuses on serving Git.

A key feature in both modes is **application-layer end-to-end encryption of API
payloads** using NaCl box (curve25519 + XSalsa20 + Poly1305). Even when a gateway
terminates TLS and can see your HTTP traffic in the clear, it still cannot read
the sealed bodies of GiGot requests and responses.

---

## Table of Contents

0. [Roadmap / TODO](#roadmap--todo)
1. [Quick Start](#quick-start)
2. [Formidable-Context Scaffolding](#formidable-context-scaffolding)
2. [Command-Line Interface](#command-line-interface)
3. [Configuration Reference (`gigot.json`)](#configuration-reference-gigotjson)
4. [On-Disk Data Layout](#on-disk-data-layout)
5. [Authentication Overview](#authentication-overview)
6. [End-to-End Encrypted Bodies](#end-to-end-encrypted-bodies)
7. [Client Enrollment Flow](#client-enrollment-flow)
8. [Admin UI and Admin API](#admin-ui-and-admin-api)
9. [HTTP API Reference](#http-api-reference)
10. [Git Smart-HTTP Endpoints](#git-smart-http-endpoints)
11. [Deployment Modes](#deployment-modes)
12. [Security Model and Tradeoffs](#security-model-and-tradeoffs)
13. [Development and Testing](#development-and-testing)
14. [Project Structure](#project-structure)

---

## Roadmap / TODO

Open work, in rough priority order. This list mirrors the in-project task
tracker and is the source of truth for "what's next."

- [ ] **Mirror-sync gigot repos to external remotes.** Per-repo upstream URL
      + credential (e.g. GitHub / Azure DevOps PAT) stored in an encrypted
      store. Post-receive hook installed in each bare repo that fires
      `git push upstream` after every accepted push. Admin UI: add/edit
      upstream per repo, show last-sync status.
- [ ] **Gateway-trusted identity strategy.** A third `auth.Strategy`
      alongside `TokenStrategy` and `SessionStrategy` that trusts a signed
      identity header forwarded by a fronting gateway (e.g. Azure APIM).
      Lets the admin UI skip server-side login when deployed behind a
      gateway that already authenticates the caller.
- [ ] **Structured sync API (multi-phase).** Move Formidable clients off
      direct `git push/pull` onto an HTTP-only content API so non-technical
      teammates cannot brick a repo with a stale push. Design and phased
      plan live in [`docs/design/structured-sync-api.md`](docs/design/structured-sync-api.md).
      The previously-listed "Formidable context marker file" task is folded
      into Phase 0 of that plan.
- [ ] **Clone-as-Formidable.** Today `POST /api/repos` rejects `source_url`
      + `scaffold_formidable: true` as mutually exclusive. Lift that
      restriction so an external repo can be cloned and stamped with
      `.formidable/context.json` in one request (second commit on top of
      the clone, authored by the scaffolder identity). Needed before any
      Formidable-first F-phase starts gating on the marker; harmless to
      defer until then.
- [ ] **NaCl-challenge admin login.** Replace the password+session login
      with curve25519 challenge/response, admin keypair held in the
      browser (passphrase-encrypted in localStorage). Password path stays
      available as a fallback. Requires vendoring `tweetnacl-js`.
- [ ] **HA-friendly admin sessions.** Persist sessions in a sealed store
      instead of in-memory so admins don't re-login after every restart.

Done and shipping:

- [x] Leaf `internal/crypto` NaCl-box package + on-disk keypair bootstrap
- [x] Client enrollment endpoint
- [x] Sealed-body middleware for `/api/*`
- [x] Encrypted persistent token store
- [x] Admin page + password/session login
- [x] Per-repo access on subscription keys (enforced via `internal/policy`)
- [x] `./gigot --rotate-keys` with atomic rewrap + backups
- [x] Central `policy.Evaluator` + `DenyAll` / `AllowAuthenticated` /
      `TokenRepoPolicy`
- [x] Models split per concern (`models_*.go`)
- [x] Roles ripped out end-to-end
- [x] Sidebar-layout admin UI with deep-linkable panels
- [x] Optional Formidable-context scaffold on repo creation
- [x] Graceful SIGINT/SIGTERM shutdown via `http.Server.Shutdown`, with a
      stale-port startup error pointing at `lsof -iTCP:<port>`

---

## Quick Start

```bash
# 1. Build
go build -o gigot .

# 2. Generate a default config next to the binary
./gigot --init
# → Wrote default gigot.json

# 3. Create your first admin account
./gigot --add-admin alice
# Password for alice:
# Confirm password:
# → Admin "alice" saved

# 4. Run the server
./gigot
# GiGot server starting on 127.0.0.1:3417
# Repository root: ./repos
# Admin UI: http://127.0.0.1:3417/admin
```

Now point a browser at `http://127.0.0.1:3417/admin`, log in as `alice`, and
issue a subscription key. Hand that key to a Formidable client to grant access.

---

## Formidable-Context Scaffolding

When you create a new repo on the admin page, you can tick **Scaffold as
Formidable context**. The checkbox defaults **off** — a vanilla empty bare
repo is what you get without it. With the box ticked, the fresh repo is
seeded with one initial commit containing the directory layout
[Formidable](https://github.com/petervdpas/Formidable) expects:

```
README.md
templates/
  basic.yaml    # minimal starter: GUID + text with `collection: entries`
storage/
  .gitkeep      # empty placeholder so the dir is tracked
.formidable/
  context.json  # marker: { version, scaffolded_by, scaffolded_at }
```

The static files live as real files in the GiGot source tree under
`internal/server/scaffold/formidable/` and are embedded into the binary via
`//go:embed all:scaffold/formidable`. The marker file `.formidable/context.json`
is generated at scaffold time so its `scaffolded_at` timestamp is accurate;
a `formidable_first: true` server reads it to decide whether a given repo
gets schema-aware sync behaviour (see
[`docs/design/structured-sync-api.md`](docs/design/structured-sync-api.md) §2.5).
To change the static starter content, edit the embedded files and rebuild —
no Go string literals to maintain.

The scaffold commit is authored and committed by
`GiGot Scaffolder <scaffold@gigot.local>` (hardcoded). Every subsequent
commit comes from whichever Formidable client pushed it, carrying that
client's real git identity — GiGot does not rewrite pushed commits.

You can also trigger scaffolding from the API directly:

```bash
curl -X POST http://localhost:3417/api/repos \
  -H 'Content-Type: application/json' \
  -b /tmp/gigot-admin-cookie \
  -d '{"name":"my-templates","scaffold_formidable":true}'
```

Verify what landed in a new repo (bare, so you need ls-tree or a clone):

```bash
git -C repos/my-templates.git ls-tree -r HEAD --name-only
# README.md
# storage/.gitkeep
# templates/basic.yaml
```

---

## Command-Line Interface

The `gigot` binary has one daemon mode and two management subcommands. Flags are
parsed via Go's standard `flag` package, so any order works.

| Flag                          | Description                                                                                                   |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `--config <path>`             | Path to a `gigot.json`. Defaults to `./gigot.json`. Missing file falls back to built-in defaults.             |
| `--init`                      | Writes a default `gigot.json` into the current directory and exits. Will not overwrite by accident — you own the file. |
| `--add-admin <username>`      | Creates (or overwrites) an admin account with the given username and exits. Prompts for a password on stdin. |
| `--rotate-keys`               | Generates a fresh server keypair, re-encrypts all sealed stores under it, backs up the previous files as `.bak.{timestamp}`, and exits. **Stop the server first.**         |

### Examples

```bash
# Generate a default config
./gigot --init

# Run with a non-default config path
./gigot --config /etc/gigot/gigot.json

# Rotate the server keypair (e.g. after a suspected leak, or before making
# the repo public). Stop the server first.
./gigot --rotate-keys

# After you've confirmed the rotated server works (admin login succeeds,
# clients reconnect), purge the rollback backups. The old server.key.bak.*
# is the leaked key you just rotated away from; do not leave it on disk.
rm data/*.bak.*

# Add an admin non-interactively (e.g. from a deploy script)
printf 'hunter2\nhunter2\n' | ./gigot --add-admin ci-admin
```

> The password prompt uses `golang.org/x/term` when stdin is a TTY (so nothing is
> echoed). When stdin is piped (CI, scripts), GiGot falls back to line-based
> reads, which is why you pipe the password twice — once for the prompt, once
> for the confirmation.

---

## Configuration Reference (`gigot.json`)

A full `gigot.json` looks like this:

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 3417
  },
  "storage": {
    "repo_root": "./repos"
  },
  "auth": {
    "enabled": false,
    "type": "token"
  },
  "crypto": {
    "private_key_path": "./data/server.key",
    "public_key_path":  "./data/server.pub",
    "data_dir":         "./data"
  },
  "logging": {
    "level": "info"
  }
}
```

All relative paths are resolved relative to the directory that contains the
config file, not the process's working directory. This makes it safe to invoke
`gigot --config /etc/gigot/gigot.json` from anywhere.

### `server`

| Field | Type   | Default       | Description                                               |
| ----- | ------ | ------------- | --------------------------------------------------------- |
| host  | string | `"127.0.0.1"` | Bind address. Set to `0.0.0.0` to accept external traffic. |
| port  | int    | `3417`        | TCP port.                                                  |

### `storage`

| Field     | Type   | Default     | Description                                                                  |
| --------- | ------ | ----------- | ---------------------------------------------------------------------------- |
| repo_root | string | `"./repos"` | Directory containing bare Git repositories. Created on demand per repo name. |

### `auth`

| Field   | Type   | Default   | Description                                                                                                                    |
| ------- | ------ | --------- | ------------------------------------------------------------------------------------------------------------------------------ |
| enabled | bool   | `false`   | Master switch for bearer-token authentication on the `/api/*` and `/git/*` paths. When `false`, all non-admin endpoints are open. |
| type    | string | `"token"` | Reserved for future strategy selectors. Currently only `token` is meaningful; session auth for the admin UI is always on.      |

### `crypto`

| Field             | Type   | Default              | Description                                                                                                                                 |
| ----------------- | ------ | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| private_key_path  | string | `"./data/server.key"` | The server's curve25519 private key, base64-encoded in a 0600 file. Generated automatically on first run if missing.                       |
| public_key_path   | string | `"./data/server.pub"` | Matching public key in a 0644 file. Also generated on first run.                                                                           |
| data_dir          | string | `"./data"`            | Where encrypted stores live: `clients.enc` (enrolled Formidable clients), `tokens.enc` (subscription keys), `admins.enc` (admin accounts). |

### `logging`

| Field | Type   | Default  | Description                                                 |
| ----- | ------ | -------- | ----------------------------------------------------------- |
| level | string | `"info"` | Log level (reserved — current code uses standard `log.*`). |

### Partial configs

The loader merges your config into the built-in defaults, so you can keep your
`gigot.json` minimal. For example, to just override the port and repo root:

```json
{
  "server":  { "port": 4000 },
  "storage": { "repo_root": "/var/lib/gigot/repos" }
}
```

---

## On-Disk Data Layout

After `--init` and a first run, you'll see something like:

```
./
├── gigot                     # the binary
├── gigot.json                # config
├── repos/                    # bare git repositories (storage.repo_root)
│   └── my-templates.git/
└── data/                     # (crypto.data_dir)
    ├── server.key            # 0600 — NaCl private key (base64)
    ├── server.pub            # 0644 — NaCl public key  (base64)
    ├── clients.enc           # sealed: enrolled clients + their pubkeys
    ├── tokens.enc            # sealed: issued subscription keys
    └── admins.enc            # sealed: admin accounts + bcrypt hashes
```

The three `.enc` files are NaCl-sealed to the server's own public key. **Only a
GiGot process holding the matching `server.key` can read them.** If you lose
`server.key`, you lose every admin account, every subscription key, and every
enrolled client pubkey — there is no recovery.

**Back up `server.key` somewhere safe.**

---

## Authentication Overview

GiGot ships three authentication strategies, all pluggable via a shared
`auth.Provider`:

| Strategy   | Where it applies                                | Credential                                        |
| ---------- | ----------------------------------------------- | ------------------------------------------------- |
| `token`    | `/api/*` (except bootstrap paths) and `/git/*` | `Authorization: Bearer <token>` header            |
| `session`  | `/api/admin/*` and the `/admin` UI              | `gigot_session` cookie set by `/admin/login`      |
| *(gateway)* | Future (Task 7): trust a signed header from a fronting gateway | To be configured                  |

### When is auth enforced?

- With `auth.enabled = false` (the default), only admin endpoints are gated.
  The rest of the API is open — convenient for development, dangerous in
  production.
- With `auth.enabled = true`, any request that is not on the public-paths list
  must present a valid bearer token or session cookie.

### Public paths (never require auth)

- `GET /` — status page
- `GET /api/crypto/pubkey` — bootstrap: clients fetch the server pubkey here
- `POST /api/clients/enroll` — bootstrap: clients register their pubkey here
- `GET /api/admin/session` — returns 401 internally when not logged in (the UI
  uses this to decide whether to show the login form)
- `/admin`, `/admin/`, `/admin/login`, `/admin/logout` — admin page endpoints
- `/swagger/*` — Swagger UI

Everything else (`/api/health`, `/api/repos*`, `/api/auth/token`, `/git/*`,
`/api/admin/tokens`) requires authentication when `auth.enabled = true`.

---

## End-to-End Encrypted Bodies

GiGot can transparently **seal** request/response bodies between enrolled
Formidable clients and the server, independent of TLS. This is the main reason
the project exists in its current form: even an API gateway that terminates
TLS between the client and GiGot cannot read the payload.

### Wire format

A sealed request carries two plain HTTP headers the gateway can still see:

- `Content-Type: application/vnd.gigot.sealed+b64`
- `X-Client-Id: <enrolled client id>`

The request body is:

```
base64( nonce[24] || box.Seal(jsonBytes, recipientPub=serverPub, senderPriv=clientPriv) )
```

The response is symmetric: the server's middleware seals the handler's output
with `box.Seal(bodyBytes, recipientPub=clientPub, senderPriv=serverPriv)`,
writes `Content-Type: application/vnd.gigot.sealed+b64`, and streams the
base64-encoded result.

### Opt-in, not mandatory

The sealed middleware is **transparent**: a request that lacks both the
content-type marker and the `X-Client-Id` header is passed through as normal
JSON. This lets plain `curl` clients, Swagger UI, and the admin browser work
without understanding NaCl. Only Formidable clients that want end-to-end
encryption need to adopt the sealed format.

### Skipped paths

The sealed middleware only acts on `/api/*`. The index page, Swagger UI, and
`/git/*` (which speaks Git's own protocol) are never sealed.

### Example (pseudocode)

```ts
// 1) Fetch server pubkey (unauth, plain):
const serverPub = (await fetch('/api/crypto/pubkey').then(r => r.json())).public_key;

// 2) Enroll once:
await fetch('/api/clients/enroll', {
  method: 'POST',
  body: JSON.stringify({ client_id: 'laptop-01', public_key: myPubB64 }),
});

// 3) From now on, seal every API request:
const sealed = nacl.box(utf8(JSON.stringify(body)), nonce, serverPubRaw, myPrivRaw);
await fetch('/api/repos', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/vnd.gigot.sealed+b64',
    'X-Client-Id':  'laptop-01',
    'Authorization': 'Bearer ' + subscriptionKey,
  },
  body: base64(concat(nonce, sealed)),
});
```

---

## Client Enrollment Flow

A Formidable client becomes known to the server in two steps:

1. **Fetch the server's public key** (unauthenticated):
   ```
   GET /api/crypto/pubkey
   → { "public_key": "<base64>" }
   ```
2. **Enroll the client's public key** (unauthenticated, once):
   ```
   POST /api/clients/enroll
   { "client_id": "laptop-01", "public_key": "<base64>" }
   → 201 { "client_id": "laptop-01", "server_public_key": "<base64>" }
   ```

Re-enrolling the same `client_id` with the **same** public key is idempotent.
Re-enrolling with a **different** public key returns `409 Conflict` — delete
the client first (future admin feature) or pick a new ID.

Enrollment does *not* grant access: it only lets the server seal responses to
that client. The admin must still issue a subscription key for the client to
hit the data endpoints.

> In a production deployment you typically put the gateway in front of
> `/api/clients/enroll` with some out-of-band gating (an enrollment password,
> a pre-shared key, Azure APIM subscription approval, etc.) so random strangers
> can't register themselves.

---

## Admin UI and Admin API

The admin UI lives at **`/admin`** and is a single self-contained HTML+JS page.
It lets an admin log in, list issued subscription keys, issue new ones, and
revoke them.

### Bootstrap

Because the admin UI needs an account to log into, you create the first admin
with the CLI:

```bash
./gigot --add-admin alice
```

The account is stored in `data/admins.enc` (sealed), so it survives restarts.

### Session model

- `POST /admin/login` with JSON `{ "username", "password" }` returns 200 + a
  `gigot_session` HTTP-only cookie valid for 12 hours.
- `POST /admin/logout` clears the session.
- All `/api/admin/*` endpoints require the session cookie.

> `Secure` is intentionally **not** set on the session cookie by default. TLS
> is typically terminated at a fronting gateway in our deployment targets. If
> you expose GiGot directly over HTTPS you should plumb a `Secure: true` flag
> via config (not yet implemented — follow-up work).

### Admin API

| Method | Path                    | Purpose                                                                               |
| ------ | ----------------------- | ------------------------------------------------------------------------------------- |
| GET    | `/api/admin/session`    | Returns the current admin identity or 401. The page polls this on load.               |
| GET    | `/api/admin/tokens`     | Lists every issued subscription key.                                                  |
| POST   | `/api/admin/tokens`     | Issues a new subscription key. Body: `{ "username", "repos": [...] }`.                |
| PATCH  | `/api/admin/tokens`     | Changes the repo allowlist on an existing key. Body: `{ "token", "repos": [...] }`.   |
| DELETE | `/api/admin/tokens`     | Revokes a subscription key. Body: `{ "token": "<value>" }`.                            |

The legacy unauthenticated `POST /api/auth/token` still exists for backward
compatibility, but the admin UI uses `/api/admin/tokens` (session-gated) for
everything.

---

## HTTP API Reference

### Health & index

| Method | Path           | Auth | Description                                           |
| ------ | -------------- | ---- | ----------------------------------------------------- |
| GET    | `/`            | —    | HTML status page                                      |
| GET    | `/api/health`  | bearer (if enabled) | JSON `{ "status": "ok" }`                |

### Crypto & enrollment

| Method | Path                   | Auth | Description                                 |
| ------ | ---------------------- | ---- | ------------------------------------------- |
| GET    | `/api/crypto/pubkey`   | —    | Returns the server's NaCl public key.      |
| POST   | `/api/clients/enroll`  | —    | Registers a client public key. Idempotent for same key, 409 for conflicting key. |

### Repositories

| Method | Path                               | Auth   | Description                           |
| ------ | ---------------------------------- | ------ | ------------------------------------- |
| GET    | `/api/repos`                       | bearer | List all repositories                 |
| POST   | `/api/repos`                       | bearer | Create a repository (body: `{ "name" }`) |
| GET    | `/api/repos/{name}`                | bearer | Repo details                          |
| DELETE | `/api/repos/{name}`                | bearer | Delete a repo                         |
| GET    | `/api/repos/{name}/status`         | bearer | Working status                        |
| GET    | `/api/repos/{name}/branches`       | bearer | List branches                         |
| GET    | `/api/repos/{name}/log`            | bearer | Commit log                            |
| GET    | `/api/repos/{name}/head`           | bearer | Current HEAD SHA + default branch     |
| GET    | `/api/repos/{name}/tree`           | bearer | Recursive blob listing at a version   |
| GET    | `/api/repos/{name}/snapshot`       | bearer | All blobs at a version (base64)       |
| GET    | `/api/repos/{name}/files/{path}`   | bearer | One blob at a version (base64)        |
| PUT    | `/api/repos/{name}/files/{path}`   | bearer | Write one file with fast-forward/auto-merge/409-conflict semantics |
| POST   | `/api/repos/{name}/commits`        | bearer | Atomic multi-file commit (put/delete ops); transactional 409 on any conflict |
| GET    | `/api/repos/{name}/changes`        | bearer | Paths added/modified/deleted between a client's `since` version and current HEAD |

### Tokens (legacy)

| Method | Path                | Auth   | Description               |
| ------ | ------------------- | ------ | ------------------------- |
| POST   | `/api/auth/token`   | bearer | Issue a token (legacy).   |
| DELETE | `/api/auth/token`   | bearer | Revoke a token (legacy).  |

### Admin

See [Admin UI and Admin API](#admin-ui-and-admin-api) above.

### Swagger

A full, machine-generated OpenAPI spec lives at `/swagger/index.html`. The raw
JSON and YAML are in `docs/`.

---

## Git Smart-HTTP Endpoints

GiGot speaks Git's smart-HTTP protocol so a repo can be cloned and pushed to
like any other remote. These endpoints sit under `/git/{name}/...`:

| Method  | Path                                 | Description                      |
| ------- | ------------------------------------ | -------------------------------- |
| GET     | `/git/{name}/info/refs`              | Ref advertisement.               |
| POST    | `/git/{name}/git-upload-pack`        | Fetches (`git clone`, `git fetch`). |
| POST    | `/git/{name}/git-receive-pack`       | Pushes (`git push`).              |

Example:

```bash
git clone http://alice:<subscription-key>@gigot.example.com/git/my-templates
```

Git endpoints **do not** participate in the sealed-body layer — Git has its own
wire protocol that can't be wrapped. Rely on TLS between the client and the
server (or gateway) for confidentiality here.

---

## Deployment Modes

### 1. Standalone

Run the binary directly on a host, optionally behind nginx/caddy for TLS:

```bash
./gigot --config /etc/gigot/gigot.json
```

Point `gigot.json` at a persistent data directory:

```json
{
  "server":  { "host": "0.0.0.0", "port": 3417 },
  "storage": { "repo_root": "/var/lib/gigot/repos" },
  "auth":    { "enabled": true, "type": "token" },
  "crypto":  {
    "private_key_path": "/var/lib/gigot/data/server.key",
    "public_key_path":  "/var/lib/gigot/data/server.pub",
    "data_dir":         "/var/lib/gigot/data"
  }
}
```

Suggested systemd unit:

```ini
[Unit]
Description=GiGot — git-backed server for Formidable
After=network-online.target

[Service]
Type=simple
User=gigot
WorkingDirectory=/var/lib/gigot
ExecStart=/usr/local/bin/gigot --config /etc/gigot/gigot.json
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### 2. Behind Azure API Management (or similar)

The gateway takes over TLS termination, caller identity (OAuth, subscription
keys, etc.), rate limiting, and coarse routing. GiGot's own auth can usually
stay **off** for `/api/*` in this mode, because the gateway is doing that
job — but note that means the gateway is trusted.

Recommended APIM configuration:

- Require an APIM subscription key on every route GiGot exposes.
- Apply rate-limit and quota policies in APIM, not in GiGot.
- **Don't** try to add WAF rules on request bodies for `/api/*` — bodies are
  NaCl-sealed and look like opaque base64 to the gateway. You can still inspect
  headers and URLs.
- For `/git/*`, let APIM pass through as-is; Git's protocol is binary and
  multiplexed.
- Forward `Authorization`, `X-Client-Id`, and `Content-Type` headers verbatim
  so the sealed-body middleware and bearer-auth strategy still work.

The planned gateway-trust strategy (Task 7) will let GiGot accept a signed
identity header from APIM so admin-UI actions can be performed without a second
login when you're already authenticated at the gateway.

---

## Security Model and Tradeoffs

- **Server keypair is a single point of failure.** Losing `server.key` invalidates
  every encrypted store. Back it up, and preferably keep a copy offline.
- **Rotation is a one-liner.** If you suspect a leak (or are about to flip a
  previously-private repo public), stop the server and run
  `./gigot --rotate-keys`. It generates a fresh keypair, decrypts every sealed
  store with the old key in memory, re-encrypts under the new one, and backs up
  the previous files as `.bak.{timestamp}` so you can inspect or roll back.
  Admin accounts, subscription tokens, and enrolled client pubkeys all survive.
  Formidable clients pick up the new server pubkey on their next
  `/api/crypto/pubkey` fetch and keep working.
- **Delete rotation backups once you're satisfied.** After `--rotate-keys`,
  `data/server.key.bak.{timestamp}` still contains the *old* private key —
  which is exactly the key material you rotated away from. Keeping it defeats
  the rotation. Once the server comes back up, an admin can log in, and any
  Formidable clients reconnect, run `rm data/*.bak.*` to purge the backups.
  They are rollback-only insurance; they are not ongoing history.
- **In-memory sessions.** Admin sessions live in process memory and are lost on
  restart — admins re-log in. This is deliberate: it avoids persisting session
  tokens at all. It means the admin UI is not HA-friendly yet.
- **Bearer tokens are opaque, not JWTs.** GiGot issues random 32-byte tokens
  that are looked up server-side. They can be revoked. They do not carry claims.
  Each token is bound to an **allowlist of repositories** the bearer may
  access (see below); management actions (creating repos, issuing keys,
  managing admins) are reserved for admin sessions.
- **Per-repo scoping is enforced centrally.** `internal/policy.TokenRepoPolicy`
  gates every `/api/repos/*` and `/git/*` route. A token with an empty
  `repos` allowlist can authenticate but cannot read or clone anything. An
  admin assigns the allowlist at issue time (`POST /api/admin/tokens`) or
  later (`PATCH /api/admin/tokens`). Listing (`GET /api/repos`) returns
  only the assigned set to token callers; admins see everything.
- **bcrypt cost.** `bcrypt.DefaultCost` (10) is used for admin passwords. Adjust
  in `internal/admins/store.go` if your hardware warrants it.
- **NaCl box, not OpenPGP.** Despite occasional shorthand, the crypto used is
  not PGP. It's NaCl's authenticated public-key encryption (curve25519 +
  XSalsa20 + Poly1305), chosen for its small, misuse-resistant API.
- **Sealed bodies block gateway content inspection.** This is the whole point.
  If you need a gateway WAF to inspect payloads, do it before sealing (i.e. in
  the client) or don't seal those routes.
- **`auth.enabled = false` is for dev only.** With auth off, anyone who can
  reach the port can list/create/delete repos.

---

## Development and Testing

### Run all tests

```bash
go test ./...
```

### Run only the integration (Cucumber/godog) suite

```bash
go test ./integration/...
```

All scenarios live under `integration/features/*.feature`. Step definitions are
in `integration/integration_test.go`. When you add a handler, consider adding a
feature file alongside a unit test so behaviour is covered at both levels.

### Regenerate the Swagger spec

```bash
swag init -g main.go -o docs
```

### Useful test paths

- `internal/crypto/*_test.go` — NaCl box roundtrips, tamper detection, on-disk keypair.
- `internal/clients/*_test.go` — enrollment store, idempotent re-enrollment.
- `internal/auth/*_test.go` — token strategy, session strategy, sealed token persister.
- `internal/admins/*_test.go` — admin store + bcrypt verify.
- `internal/server/*_test.go` — HTTP handlers, index page, repo router.
- `integration/features/*.feature` — end-to-end scenarios for every route.

---

## Project Structure

```text
GiGot/
├── main.go                           # Entry point
├── gigot.json                        # Server config (generated with --init)
├── cmd/gigot/root.go                 # CLI bootstrap, flag parsing, --add-admin
├── docs/                             # Generated Swagger assets
├── integration/                      # Cucumber feature tests
│   ├── integration_test.go
│   └── features/*.feature
└── internal/
    ├── admins/                       # Admin account store (bcrypt + sealed file)
    ├── auth/                         # Provider, TokenStrategy, SessionStrategy, SealedTokenStore
    ├── clients/                      # Enrolled client pubkeys (sealed file)
    ├── config/                       # JSON config loading + defaults
    ├── crypto/                       # NaCl box wrappers + keypair bootstrap (leaf package)
    ├── git/                          # Bare repo management
    └── server/                       # HTTP server, routes, middleware, admin page
        ├── server.go                 # Wiring
        ├── middleware_sealed.go      # Sealed-body request/response middleware
        ├── handler_admin.go          # Admin auth + token management
        ├── handler_admin_page.go     # /admin HTML+JS
        ├── handler_clients.go        # Client enrollment
        ├── handler_crypto.go         # Server pubkey
        ├── handler_auth.go           # Legacy token endpoints
        ├── handler_repos.go          # Repository CRUD
        ├── handler_health.go         # /api/health
        ├── handler_git.go            # Git smart-HTTP proxy
        ├── router.go                 # Sub-routers for /api/repos and /git
        ├── respond.go                # JSON helpers
        └── models.go                 # Request/response DTOs
```

Every package aims to keep one clear responsibility. `internal/crypto` is a leaf
package with no imports from other internal packages so it can be reused (and
tested) without dragging the rest of the server in.
