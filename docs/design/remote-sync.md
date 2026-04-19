# Remote sync — does GiGot need it, and if so, what shape?

Status: open question, not a committed feature. This document exists to
decide whether mirror-sync to external remotes belongs in GiGot at all
before any code gets written, and — if it does — to pin down the design
shape that the current README roadmap entry under-specifies.

---

## 1. The actual question

The README roadmap says:

> Per-repo upstream URL + credential (e.g. GitHub / Azure DevOps PAT)
> stored in an encrypted store. Post-receive hook installed in each bare
> repo that fires `git push upstream` after every accepted push.

That wording quietly assumes two things we have not examined:

1. **That remote sync should exist at all.**
2. **That one upstream per repo is enough.**

Both are worth challenging before building anything.

---

## 2. Do we need remote sync at all?

### 2.1 Case for

- **Disaster recovery.** If the host running GiGot dies, the data still
  exists somewhere else.
- **Compliance / retention.** Some users will have a policy-level need to
  store a copy in a specific system (a corporate Azure DevOps, a
  self-hosted Gitea, etc.).
- **Ecosystem integration.** Mirroring to GitHub means existing GitHub
  workflows (Actions, Pages, Dependabot) keep working against the mirror
  without GiGot having to reinvent them.
- **User peace of mind.** A user who already backs up their life to
  GitHub is going to want their Formidable data there too, regardless of
  whether it's technically redundant.

### 2.2 Case against (take seriously)

- **GiGot is already the backup.** The primary copy lives on each
  Formidable client. GiGot is the server-side copy. Mirroring to GitHub
  makes GitHub a backup-of-a-backup. Ask honestly: what failure does
  that protect against that the distributed clients don't already
  cover? A laptop dying is survived by GiGot. GiGot dying is survived
  by any laptop pushing to a new GiGot. Adding a third leg only helps
  when *both* GiGot and every laptop are lost at once — a rare scenario
  compared to the operational cost of running the mirror.
- **It undermines the sealed-body story.** GiGot's main selling point is
  that payloads between Formidable and GiGot are NaCl-sealed — even a
  TLS-terminating gateway cannot read them. But a mirror push to GitHub
  ships **plaintext git objects**. The moment you enable mirror-sync to
  a third party, that third party sees everything in the clear. Users
  who care about privacy enough to choose GiGot are exactly the users
  who should not be mirroring plaintext to GitHub. This is not a
  footnote — it is the main architectural tension.
- **Operational cost.** Every mirror push is a new failure mode: rate
  limits, expired PATs, network partitions, remote renames. Each
  produces a new "why didn't my push mirror?" support surface and a new
  "is my data actually safe?" question.
- **Credential sprawl.** Multi-remote mirroring means multi-credential
  storage, rotation, revocation, and admin UI. All of that is new
  attack surface proportional to how many destinations we support.
- **Feature-creep signal.** No individual Formidable user has actually
  asked for this. The roadmap entry reads like a developer-brained
  "it'd be cool if." That is the right time to push back, not after
  implementation.

### 2.3 Proposed verdict

Default answer: **no, don't build it yet.** Leave remote sync out until a
real user names the specific scenario they need it for. The scenarios
above are plausible but not currently grounded in a concrete demand.

If the answer turns out to be yes — proceed to §3.

---

## 3. If we do build it — the shape

This section exists only to make sure that when remote sync *does* get
built, it solves the right problem. None of it is committed work.

### 3.1 Multi-remote, not single-upstream

The README's "one upstream per repo" is too narrow. A realistic user
wants one repo mirrored to two or three places at once — e.g. GitHub
for convenience, self-hosted Gitea for sovereignty, Azure DevOps for
corporate compliance. Each destination has its own URL, its own
credential, and can fail independently.

Shape:

```
Repo "addresses" → [
  { url: "https://github.com/alice/addresses.git",          credential: <GH-PAT>,   enabled: true },
  { url: "https://dev.azure.com/corp/_git/addresses",       credential: <AZDO-PAT>, enabled: true },
  { url: "https://gitea.example.com/alice/addresses.git",   credential: <Gitea-key>, enabled: false },
]
```

Per destination we store: URL, credential, enabled flag, last-sync
timestamp, last-sync status, last-sync error. No shared credentials
across destinations — that removes any "which PAT does this repo use?"
ambiguity.

### 3.2 Push-only, not bidirectional

Bidirectional sync would require GiGot to reconcile commits that land
at the remote without going through GiGot first — a merge problem
GiGot has no reason to own. Mirror sync is **push-only**: GiGot is the
source of truth; the remotes are follower copies. If someone pushes
directly to the GitHub mirror, GiGot will reject that mirror's state
on the next sync (fast-forward required). That's the right failure
mode: if a user wanted GitHub-first flow, they wouldn't be running
GiGot.

### 3.3 When to push

Two options:

- **Per-push (synchronous).** Fires from a post-receive hook on the
  bare repo immediately after every accepted client push. Low lag,
  tight coupling. A slow remote slows every client push.
- **Per-push (async queue).** Same trigger, but enqueue the destination
  pushes and fire them from a worker. Decouples client latency from
  mirror latency. Adds a queue + worker to operate.

Default: async queue. Individual Formidable pushes must not block on
GitHub's availability.

### 3.4 Failure handling

A mirror push fails. Options:

- **Silent-and-log.** Retry a few times with backoff, then leave the
  destination in an error state visible in the admin UI. Client pushes
  still succeed. Administrator sees the red status and acts.
- **Hard-fail.** Reject the client push if any enabled mirror fails.
  Makes GiGot only as available as its least-available mirror. Bad
  choice for a user-facing app.

Default: silent-and-log. Admin UI surfaces per-destination health.

### 3.5 Credential storage

Credentials go in the existing sealed store alongside tokens and admin
accounts. Rotation story is the same as for tokens — including the
`-rotate-keys` path that already re-wraps every sealed file. No new
key material design needed.

### 3.6 Admin UI

One table per repo: destination list with status, plus add/edit/delete
per row. No "bulk" operations until a real case calls for them.

### 3.7 Privacy reminder

Anyone enabling mirror sync to a third party is turning off GiGot's
sealed-body advantage for that repo. The admin UI must say this in
plain language when a destination is added — not buried in docs. The
user should have to check a box that says "I understand the contents
of this repo will be readable at the destination."

---

## 4. Open questions

- Is there a sovereign-friendly variant where GiGot pushes to a
  destination that stores the repo encrypted-at-rest under a key only
  the user's devices hold? (Much bigger scope. Probably a separate
  feature, not part of this one.)
- Should destinations support fetch-from as well (seed GiGot from a
  pre-existing GitHub repo)? Today that happens at repo creation via
  `source_url`. A "re-seed from destination" operation would be a
  separate admin action, not part of the mirror feature.
- Does mirror-sync need to know about the Formidable-first marker at
  all? No — it ships git objects byte-for-byte. Formidable structure
  is orthogonal.

---

## 5. Decision checklist

Before committing to build this, we should be able to answer:

- [ ] What concrete user need are we solving that isn't already covered
      by the fact that each client holds a full clone?
- [ ] Are we okay telling users "the moment you enable this, your data
      is readable at the destination"?
- [ ] Who operates the destinations, and who rotates their credentials
      when they leak?
- [ ] What does the admin UI show when a mirror has been broken for a
      week? What does the user do about it?

If those don't have good answers, don't build it.
