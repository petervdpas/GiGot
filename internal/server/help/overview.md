# Overview

GiGot is a Git-backed server for Formidable. One operator runs it;
one or more teams use it through their own repos to push and pull
templates, records, and images without anyone needing to set up Git
infrastructure of their own.

## What it does

- Stores each Formidable context as a plain Git repository, served
  over Smart-HTTP at `https://<server>/git/<repo>`.
- Issues **subscription keys** that are *one per user, per repo* —
  never one shared key for a whole team. Every key can read and push
  to its repo by default; opt-in abilities such as `mirror` (manage
  the repo's mirror destinations) are added explicitly. A user
  working on two repos gets two keys, one each.
- Optionally **mirrors** every push to one or more remote
  destinations (GitHub, Azure DevOps) so the team's data has an
  off-server copy.
- Encrypts request and response bodies end-to-end between enrolled
  Formidable clients and the server, independent of TLS.
- Exposes a small admin console for repos, subscription keys,
  outbound credentials, and accounts.

## What it does not do

- It is **not** a general-purpose Git host. Repos exist to serve
  Formidable contexts; there is no fork / PR / issue surface.
- It does **not** know about Formidable's field schemas. Per-record
  merge is uniform (`data.*` is one atomic value per field, last
  writer by `meta.updated` wins); structural validation is
  Formidable's concern.

## Who it's for

Teams who already use Formidable and want shared records without each
member having to run Git locally.

| Role | What they do |
| -- | -- |
| Operator | Runs the server, manages repos, hands out subscription keys |
| Admin | Same as Operator but via the web UI instead of the CLI |
| User | Configures Formidable with a subscription key; never logs in to GiGot directly unless they want to see their own keys at [/user](/user) |

## How teams share a deployment

A GiGot deployment is **team-agnostic**: the operator decides how
many teams it serves by deciding how many repos to create. The
**repo is the team boundary**, not the server.

A few common shapes:

- **One team, one repo.** A small team with a single Formidable
  context. The simplest case.
- **One team, several repos.** Different working sets — for example
  one repo for shared templates and a separate one per project — all
  used by the same group of people.
- **Several teams, separate repos.** Two or more teams running on
  the same GiGot. Team A pushes to `repo-a`, team B pushes to
  `repo-b`; subscription keys are scoped to one repo each, so no key
  ever touches another team's data even though both teams sign in to
  the same admin console.

Adding a team to an existing deployment means creating their repo
and issuing **one subscription key per member** — never one shared
key for the whole team. Per-user keys keep the audit trail honest
(every push is attributable to a person, not a mailbox), let you
revoke a single member without disrupting the rest, and stop a
leaked key from compromising more than one account.

Removing a team means deleting their repos and revoking their keys.

## Where to go from here

- A user joining a team — [Signing in](/help/signing-in)
- An operator standing up a new server — [Operator setup](/help/operator-setup)
- A developer integrating with the API — [API documentation](/swagger/index.html)
- The full reference — [README on GitHub](https://github.com/petervdpas/GiGot#readme)
