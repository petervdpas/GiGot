# Operator setup

> *Stub — fill me in.*

This page is for the person running the GiGot server. End users should
read [Signing in](/help/signing-in) instead.

## First boot

1. Install the binary or pull the Docker image (`petervdpas/gigot`).
2. Drop a `gigot.json` next to it (see the README's
   [Configuration Reference](https://github.com/petervdpas/GiGot#configuration-reference-gigotjson)).
3. Start the server. On first boot it generates an encryption keypair
   and seeds the configured admin accounts.
4. Sign in at [/signin](/signin) and set a password for the seeded admin
   if one wasn't provided in the config.

## Common day-2 tasks

- **Issue a subscription key** — [Subscription keys](/admin/subscriptions),
  pick a repo + an account, choose abilities.
- **Connect a mirror destination** — [Repositories](/admin/repositories),
  expand a repo's "Mirror" section.
- **Rotate keys** — `gigot -rotate-keys` (atomic; old keys backed up).
- **Reset state** — `gigot -wipe-*` (granular wipe per concern; see
  `gigot -help`).

## Backups

The state directory (default `~/.gigot/`) holds the encrypted vaults
(`tokens.enc`, `accounts.enc`, `credentials.enc`, `destinations.enc`,
`sessions.enc`) and the repo root. Back them up together — the keypair
in `private.pem` is required to decrypt the rest.
