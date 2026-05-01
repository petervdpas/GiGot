# Operator setup

This page is for the person standing up the GiGot server. End users
should read [Signing in](/help/signing-in) instead. Everything here
is also covered in the [README](https://github.com/petervdpas/GiGot#readme),
in greater depth.

## First boot

1. **Get the binary.** Either download a release tarball from
   [GitHub releases](https://github.com/petervdpas/GiGot/releases) or
   pull the Docker image (`petervdpas/gigot:latest`).
2. **Generate a config.** Run `./gigot -init` to write a
   `gigot.json` next to the binary with sensible defaults
   (port `3417`, `./repos`, `./data`). Edit it before the first
   real start — at minimum set `auth.enabled: true` and a bootstrap
   admin under `admins`.
3. **Start the server.** First boot generates a NaCl keypair at
   `./data/server.key` + `./data/server.pub` and seeds the
   configured admin accounts as passwordless rows.
4. **Set the bootstrap admin password.** Run
   `./gigot -add-admin <username>` (the username from your
   `admins` block) and enter a password when prompted.
5. **Sign in** at [/signin](/signin) and continue from the admin
   console.

## Common day-to-day tasks

| Task | Where |
| -- | -- |
| Create a repo | [Repositories](/admin/repositories) — **Create** |
| Issue a subscription key | [Subscription keys](/admin/subscriptions) — pick repo + account, choose abilities |
| Add a mirror destination | [Repositories](/admin/repositories) — expand a repo's *Mirror* section |
| Manage credentials | [Credentials](/admin/credentials) — used by mirror targets and OAuth provider configs |
| Promote / demote a user | [Accounts](/admin/accounts) |
| Hot-reload auth config | [Authentication](/admin/auth) — edit auth strategies without a restart |

## CLI reference

Every flag below is one-shot — the server is not running while it
executes. Run `./gigot -help` for the full list with grouped help.

```text
-init                   Write a fresh gigot.json next to the binary.
-add-admin <username>   Create or update an admin account; prompts for a password.
-rotate-keys            Rotate the NaCl keypair and rewrap every sealed store atomically.
-wipe-repos             Delete every bare repo under storage.repo_root.
-wipe-tokens            Delete data/tokens.enc (all subscription keys).
-wipe-credentials       Delete data/credentials.enc.
-wipe-destinations      Delete data/destinations.enc.
-wipe-sessions          Delete data/sessions.enc (boot all signed-in users).
-wipe-clients           Delete data/clients.enc (re-enrol all Formidable clients).
-wipe-admins            Delete data/accounts.enc + the legacy data/admins.enc.
-factory-reset          All -wipe-* targets at once.
-add-demo-setup         Provision the Postman demo admin + repo + key.
-healthcheck            Probe the configured port; exit 0 when healthy.
-version                Print the build version stamp.
```

The `-wipe-*` family takes `-yes` to skip the confirmation prompt
(useful for scripted resets in test environments — never in
production).

## Backups

Back up the **data directory** (`storage.data_dir`, default
`./data`) and the **repo root** (`storage.repo_root`, default
`./repos`) together. Without `data/server.key` the encrypted vaults
(`tokens.enc`, `accounts.enc`, `credentials.enc`, `destinations.enc`,
`sessions.enc`) cannot be decrypted, so the keypair is the
load-bearing piece — losing it locks the team out permanently.

Suggested cadence:

- Snapshot both directories on the same schedule as your other
  team-data backups.
- Test a restore at least once, on a fresh host, before you
  actually need it.
- Treat `data/server.key` like an SSH private key: not in version
  control, not on shared storage, not in chat history.

## Upgrading

GiGot is a single binary. Replace it, restart, done — the embedded
state files are forward-compatible across minor releases. The
release notes flag any required `-rotate-keys` or migration step
when one becomes necessary.

## When something is wrong

- **Logs** — the server logs every authentication decision, mirror
  outcome, and audit-trail commit to stdout. Capture stdout/stderr
  to a file in your service unit / Docker logs driver.
- **Healthcheck** — `./gigot -healthcheck` (or `docker exec gigot
  /gigot -healthcheck`) probes the configured port and exits 0 when
  the server is responsive. The Docker image runs this every 30 s
  out of the box.
- **Audit trail** — every push lands a tamper-proof commit on
  `refs/audit/main` inside the affected repo; clone with
  `git clone --mirror` and inspect to reconstruct who did what when.
