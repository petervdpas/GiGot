# Remote sync — does GiGot need it, and if so, what shape?

Status: **partly shipped.** Slices live today: slice 1 (destinations
data-model + admin API), slice 2.5 (token abilities + `mirror`
ability + subscriber-facing destinations API per §2.6), the refspec
compatibility spike (§5), slice 2a (manual Sync-now endpoint +
shared `syncOnce` helper), slice 2b (post-receive fan-out worker),
and slice 3 (admin UI with the §3.7 privacy gate and click-to-toggle
enabled badge). The §2.2 privacy tension is resolved in §2.4:
sealed-body is scoped to GiGot↔client; mirroring is a deliberate
per-destination operator opt-in. Outstanding follow-ups that didn't
make a ship slice: retries + backoff on a failed auto-push, and a
persistent queue that survives restart — both deliberately descoped
(§3.4 "silent-and-log" is enough until a real user hits the limit).

**Slice 4 — admin pull-from-remote — planned.** Adds an admin-only
**Pull from remote** button beside the existing push action so the
operator can recover after a direct-to-remote commit (GitHub web
edit, VS Code commit while GiGot was offline, contributor merge)
without losing it on the next sync. Pull is admin-UI / admin-endpoint
only; it is never exposed on the subscriber surface. See §3.2.

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

### 2.4 Resolution

§2.3's "don't build it yet" was the right default *before* a
concrete demand existed. That default has since been superseded:
slice 1 shipped to back a disaster-recovery and ecosystem-mirror
story, and the §2.2 tension is now resolved explicitly rather than
left implicit.

**Sealed-body encryption is a GiGot↔client scope claim, not a
GiGot↔everywhere claim.** A repo with zero mirror destinations
stays inside that scope — the sealed-body pitch holds end-to-end.
The moment an operator adds a destination, they are making an
explicit opt-in decision to ship plaintext git objects to that
destination. The consent mechanism is the §3.7 privacy-warning
checkbox, required per destination at add time.

This keeps the sealed-body pitch honest (it still means what it
always did for repos that don't mirror) while giving operators a
deliberate path to accept the tradeoff when they want disaster
recovery or ecosystem integration. The decision is per-repo,
per-destination, and recorded — not hidden in docs.

Implication for slices 2 and 3: no new gating code is needed in
the push worker. The consent gate lives in the admin UI
add-destination form (slice 3, §3.7); the existence of a
configured destination *is* the operator's consent. The worker
just pushes what it's told to push.

### 2.5 Two tracks, not one

Remote-destination workflows live on **two deliberately separate
tracks**. GiGot owns one; Formidable owns the other. Neither is
intended to grow into the other.

**Track A — byte-level git mirror (GiGot).** Pushes the raw git
repo byte-for-byte to another git host. Use cases: disaster
recovery, compliance archiving, ecosystem integration where the
receiving system is itself a git host (GitHub, Azure DevOps git,
self-hosted Gitea). The payload is whatever commits GiGot holds —
yaml templates, `.meta.json` records, images, blobs — delivered
unchanged. GiGot is deliberately **schema-blind** on this track.

**Track B — schema-aware publishing (Formidable, via WikiWonder
and similar plugins).** Renders Formidable records into a target
system's native schema: Azure DevOps *wiki* markdown with
`.order` files and attachment paths, Confluence pages, static-
site input, etc. Requires knowing what a record *means*, which is
the Formidable plugin's domain, not GiGot's. This track never
enters GiGot's codebase. Credentials for its destinations live in
Formidable's profile config, not in GiGot's credential vault.

Boundary consequences:

- The credential vault in `internal/credentials` exists to serve
  Track A only. If a future feature needs schema-aware publishing
  credentials, they belong in Formidable, not here.
- Any temptation to "make GiGot's mirror smarter about
  Formidable" collapses this boundary and should be refused.
  The mirror ships bytes; the plugin ships meanings.
- Users can run both tracks against the same Formidable content
  without stepping on each other: Track A gives them a git-level
  backup of the raw storage; Track B gives them a rendered wiki.

### 2.6 Provisioning and enablement

Track A is not turned on everywhere by default, and not everyone
who holds a subscription token can configure it. Two roles
interact:

1. **Admin (service operator).** Grants the `mirror` ability to
   specific subscription tokens via the admin API. Grants are
   revocable. Admins can also configure destinations directly via
   the existing admin-session-gated endpoint as an override
   / onboarding path.
2. **Subscriber (token holder).** A token that holds the `mirror`
   ability can manage its own destinations via a subscription-
   facing API — `/api/repos/{name}/destinations` with Bearer auth,
   gated by `TokenRepoPolicy` (repo in allowlist), a role check
   (`requireMaintainerOrAdmin`, added Phase 6 — see accounts.md
   §6.1), and `TokenAbilityPolicy` (`mirror` ability present). All
   three layers required; any one denies. Tokens that fail any layer
   get `403` on this surface; all other endpoints are unchanged.

The ability concept extends the existing `repos: [...]` scope on
tokens with an orthogonal `abilities: [...]` scope. It is *not*
a reintroduction of the roles system that was deliberately ripped
out: abilities are explicit claims attached to individual
credentials, not a lookup into a separate roles table. Closest
analogue is OAuth scopes or GitHub fine-grained PAT permissions.
For now there is exactly one ability name: `mirror`. Future
abilities (e.g. `credentials:read-own`) would follow the same
pattern without needing a role model.

Data-model delta:

- `TokenEntry` gains `abilities []string` (persisted in
  `tokens.enc`, rewrapped by `-rotate-keys` alongside the other
  fields — no migration needed since additive JSON is
  forward-compatible).
- `POST /api/admin/tokens` and `PATCH /api/admin/tokens` accept
  an optional `abilities` array.
- New `internal/policy.TokenAbilityPolicy` — a leaf policy
  evaluator, same shape as `TokenRepoPolicy`.
- Admin UI grows a checkbox column on the tokens table.

Implication for slices 2 and 3: the admin-session destinations
surface and the push worker do not change. The subscription-
facing destinations API is a new, additive surface (call it
*slice 2.5*) that can ship before or after the worker — the two
are orthogonal.

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

### 3.2 Push by default, admin-only pull as an escape hatch

GiGot is still the source of truth; the remotes are still follower
copies. The everyday flow is push: `Push to remote` (the green
button on the admin destination card) and the existing programmatic
sync endpoint (post-receive worker, auto-mirror, subscriber tokens
with the `mirror` ability) all run the same `git push +refs/heads/*
+refs/audit/*` against the destination. That part has not changed.

What slice 4 adds is a single admin-only escape hatch: **Pull from
remote** (the orange button, admin UI only). It fetches the
destination and force-updates the local bare repo's `refs/heads/*`
to match. It is the answer to "someone committed directly on the
remote and we want to keep that commit."

Three deliberate constraints on the pull side:

1. **Admin only.** No subscriber endpoint, no `mirror`-ability
   variant, no public API surface. The button lives on the admin
   Repositories page and calls an admin-session-gated endpoint.
   Subscribers never see it. Rationale: pulling is destructive to
   GiGot's local refs, and the operator (the person who configured
   the destination) is the only one who should decide when to
   accept the remote's state as truth.
2. **No merge.** GiGot does not produce merge commits, does not run
   three-way merges, does not surface a conflict editor. The pull
   is a force-update of local refs to match remote. If you wanted
   to preserve commits on both sides you needed a working clone
   before clicking the button; GiGot's job is to be a sync hub, not
   a merge tool.
3. **No recovery refs in v1.** The admin clicked the button. We
   don't snapshot the loser side to `refs/recovery/...` for them.
   If a real user trips on this once, we add it; until then it is
   an unwarranted complication.

Subscribers and the post-receive worker are unchanged. The
`mirror` ability still grants only push-direction power. There is
no subscriber-facing way to pull, by design.

**Tests** (slice 4):

- godog `sync.feature`: positive — admin pull advances local when
  remote has commits GiGot lacks. Positive — admin pull on a
  diverged remote force-updates local (loser commits gone, by
  design). Negative — subscriber token with `mirror` ability gets
  `404` on the admin pull path. Negative — admin pull on a
  destination with bad credentials returns the same error envelope
  as the existing push path.
- Unit (`internal/server/mirror_pull_test.go`, new): the fetch
  helper resolves the credential from the vault, runs `git fetch`
  with the expected refspec, force-updates `refs/heads/*`, returns
  the new local SHA per branch.
- Unit on the admin handler: admin session required (401 without,
  200 with), unknown repo / unknown destination return the same
  shape as the push handler.

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

Credentials live in the credential vault — `internal/credentials` +
`data/credentials.enc`, sealed to the server pubkey and rewrapped by
`-rotate-keys` alongside the other `.enc` files. The vault already
exists (see [`credential-vault.md`](credential-vault.md)); remote-sync
is simply its first consumer. A destination stores a vault **name**,
not the secret itself — rotating a PAT is a single vault update, not
a per-repo sweep.

#### 3.5.1 Why not git-credential-manager (GCM)?

GitHub, Azure DevOps, and the `git` docs all point a human at a
workstation at GCM. That is genuinely the right tool **for an
interactive user** — it stores credentials in the OS keychain
(Windows Credential Manager / macOS Keychain / libsecret) and handles
the OAuth device-flow browser popups when tokens expire.

It is not the right tool for GiGot, for three reasons:

1. **Wrong trust model.** GCM targets a desktop session. A headless
   `gigot` daemon has no keychain to write to and no browser to pop,
   so the "let GCM handle refresh" UX collapses. Running GCM under a
   service account means inventing a parallel secret-storage
   mechanism (file-backed helper, plaintext `.git-credentials`, or
   keyring-per-user on a server) — all of which are strictly worse
   than the sealed vault we already have.
2. **Two stores = two rotation stories.** The vault is rewrapped by
   `-rotate-keys` alongside `admins.enc` / `tokens.enc` /
   `clients.enc`. Pulling credentials out into GCM splits that into
   two incompatible rotation and loss-recovery paths. There is no
   user benefit to offset the operational cost.
3. **GCM solves a different problem.** The vault is a *named,
   rotatable, UI-managed bag of secrets* — an admin product surface.
   GCM is wire-protocol plumbing between `git push` and one
   destination's auth flow. They are not substitutes.

Where GCM *could* legitimately plug in is **inside** the push worker
(§3.3), as the thing that actually executes the `git push`: the
worker pulls the PAT from the vault in memory, hands it to git via a
one-shot `credential.helper=!…` shim (or `GIT_ASKPASS`), and the
secret never lands on disk. That keeps GCM where it shines (speaking
the credential-helper protocol to git) without making it the store.

### 3.6 Admin UI

One table per repo: destination list with status, plus add/edit/delete
per row. No "bulk" operations until a real case calls for them.

Per-destination action row (slice 4): two coloured buttons replace
the original single "Sync now" button.

- **Push to remote** (green) — calls the existing admin sync
  endpoint. Same code path as auto-mirror and the post-receive
  worker; the relabel is purely UI.
- **Pull from remote** (orange) — calls the new admin-only pull
  endpoint described in §3.2. Force-updates local refs to match
  the destination.

"Sync now" is removed from the admin card. The `sync` endpoint
still exists in the API for programmatic use (worker, auto-mirror,
subscribers with the `mirror` ability); only the admin button is
renamed.

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
- ~~Should destinations support fetch-from as well?~~ Answered by
  slice 4 (§3.2): yes, as an admin-only **Pull from remote**
  button. Seeding-from-scratch still happens at repo creation via
  `source_url` — pull is for the post-creation "remote got a
  commit GiGot doesn't have" case, not for first-time onboarding.
- Does mirror-sync need to know about the Formidable-first marker at
  all? No — it ships git objects byte-for-byte. Formidable structure
  is orthogonal.

---

## 5. Decision checklist

Before committing to build this, we should be able to answer:

- [ ] What concrete user need are we solving that isn't already covered
      by the fact that each client holds a full clone?
- [x] Are we okay telling users "the moment you enable this, your data
      is readable at the destination"? **Yes — see §2.4. Consent lives
      in the §3.7 privacy-warning checkbox at destination-add time.**
- [ ] Who operates the destinations, and who rotates their credentials
      when they leak?
- [ ] What does the admin UI show when a mirror has been broken for a
      week? What does the user do about it?
- [x] Does GitHub (and by extension Azure DevOps) accept a combined
      `+refs/heads/*:refs/heads/* +refs/audit/*:refs/audit/*` push?
      **Yes — confirmed against `petervdpas/Braindamage` on
      2026-04-20 with a fine-grained PAT (Contents R/W + Metadata R).
      GitHub accepted `refs/audit/*` as `[new reference]`; `git
      ls-remote` afterwards showed both `refs/heads/master` and
      `refs/audit/main` on the remote. The audit chain travels with
      the mirror without a fallback.**

If those don't have good answers, don't build it.
