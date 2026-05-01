# Overview

GiGot is a Git-backed server for Formidable. One operator can run it. One team of users can push and pull their Formidable data through it.
They can push and pull their templates, records, and images through it without anyone needing to set up their own Git host.

## What it does

- Stores each Formidable context as a plain Git repository, served
  over Smart-HTTP at `https://<server>/git/<repo>`.
- Issues per-user **subscription keys** scoped to exactly one repo
  each. Every key can read and push to its repo by default; opt-in
  abilities such as `mirror` (manage the repo's mirror destinations)
  are added explicitly.
- Optionally **mirrors** every push to one or more remote
  destinations (GitHub, Azure DevOps) so the team's data has an
  off-server copy.
- Encrypts request and response bodies end-to-end between enrolled
  Formidable clients and the server, independent of TLS.
- Exposes a small admin console for repos, subscription keys,
  outbound credentials, and accounts.

## What it does not do

- It is **not** a general-purpose Git host. Repos exist to serve
  Formidable contexts; there is no fork/PR/issue surface.
- It is **not** a multi-tenant SaaS. One server, one team, one set of
  accounts. Two teams = two GiGot deployments.
- It does **not** know about Formidable's field schemas. Per-record
  merge is uniform (`data.*` is one atomic value per field, last
  writer by `meta.updated` wins); structural validation is
  Formidable's concern.

## Who it's for

Teams who already use Formidable and want shared records without each member having to run Git locally.

| Role | What they do |
| -- | -- |
| Operator | Runs the server, manages repos, hands out subscription keys |
| Admin | Same as Operator but via the web UI instead of the CLI |
| User | Configures Formidable with a subscription key; never logs in to GiGot directly unless they want to see their own keys at [/user](/user) |

## Where to go from here

- A user joining a team — [Signing in](/help/signing-in)
- An operator standing up a new server — [Operator setup](/help/operator-setup)
- A developer integrating with the API — [API documentation](/swagger/index.html)
- The full reference — [README on GitHub](https://github.com/petervdpas/GiGot#readme)
