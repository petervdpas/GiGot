# Structured Sync API — Design & Execution Plan

**Status:** accepted, Phase 0 closed (2026-04-16), Phase 1 shipped (2026-04-16), Phase 2 shipped (2026-04-17), Phase 3 shipped (2026-04-17), Phase 4 shipped (2026-04-17). Formidable-first layer (§10–§11) and Formidable-side opt-in hierarchy (§2.6) added 2026-04-16. Marker provisioning rule (§2.7) shipped 2026-04-17. §10.3 simplified to a uniform last-writer-wins field rule 2026-04-18 (per-type merge logic removed; live-presence service will handle fine-grained co-editing later). Phase F1 shipped 2026-04-18. Phase F2 descoped 2026-04-18 (server-side schema validation would couple to Formidable; template structural merge not worth the cost).
**Owner:** Peter
**Last updated:** 2026-04-18

This document is the source of truth for moving GiGot from "dumb git remote +
sealed bodies" toward a **structured sync API** that lets Formidable clients
read and write repo content over plain HTTP, with git kept entirely
server-side. Future Claude sessions: read this before touching the sync
surface; update it when a phase lands or a decision changes.

---

## 1. Why we're doing this

The failure mode we are eliminating: a team member edits a Formidable
template, clicks Sync, and the underlying `git push` is rejected because
another teammate pushed first. The user has no git skills, no rebase UI, and
the repo effectively becomes unusable from their end.

Two constraints shape the answer:

1. **No git on the client.** Formidable must scale to non-technical users who
   will not install git, embed libgit2, or understand refs. The client speaks
   HTTP only.
2. **We own both sides.** GiGot and Formidable are developed together, so a
   protocol change that requires client work is acceptable.

### What this replaces

- End-users will never call `/git/{name}/git-upload-pack` or
  `/git/{name}/git-receive-pack` from the Formidable app.
- The existing smart-HTTP endpoints **stay** for power users, external mirrors
  (see the mirror-sync roadmap item), and CI. They are the back door, not the
  front door.

---

## 2. Design decision

GiGot exposes a set of content endpoints under `/api/repos/{name}/…` that
speak in **logical edits against a parent version**. The server:

- Serves snapshots and single-file reads at any commit.
- Accepts proposed edits with a `parent_version` (opaque commit SHA the client
  got from a previous read).
- If the parent is current HEAD → fast-forward commit.
- If the parent is stale → 3-way merge via `git merge-tree` (bare-repo safe,
  no worktree). Clean merge ⇒ auto-commit as a merge commit. Dirty merge ⇒
  `409 Conflict` with per-file conflict blobs.

The client never reasons about refs, packs, or non-fast-forwards. It holds
only files on disk and an opaque `version` string.

### Authoring identity

Every commit carries the subscription-key's username as the author. The
scaffolder identity (`GiGot Scaffolder <scaffold@gigot.local>`) stays
reserved for the initial scaffold commit and server-authored merge commits.

Concrete rule: `author = client_supplied_name <client_supplied_email>` if
present and the subscription key has permission; otherwise
`<subscription_username>@gigot.local`. Committer is always the
scaffolder identity so the server's role is auditable in `git log`.

---

## 2.5 Server modes

GiGot runs in one of two modes, selected by a single server-config key:

```
formidable_first: bool   # default: false
```

**Generic mode (`formidable_first: false`, default)** — the protocol
described in the rest of this document. File bodies are opaque; merges are
line-based via `git merge-tree`; conflict shape is blob-triples. Any client
that speaks the HTTP protocol works — Formidable, a CI script, a
third-party editor.

**Formidable-first mode (`formidable_first: true`)** — same endpoints, same
wire format, smarter behaviour. The server parses `templates/*.yaml` and
`storage/**/*.meta.json`, applies field-level merges driven by the template
schema, validates records against their templates on commit, enforces
referential integrity for image fields, and exposes a record-query
endpoint. See §10 for capabilities and §11 for the execution phases.

**Per-repo opt-in.** On a `formidable_first` server, a repo only gets the
smart behaviour if it carries the marker file `.formidable/context.json`
(see Phase 0). Non-marker repos on the same server fall back to generic
mode. This keeps the two worlds coexisting on one binary: a
Formidable-first deployment can still host a plain structured-sync repo
without the server choking on non-conforming content.

**Why two modes at all.** GiGot's protocol is useful beyond Formidable —
structured sync with conflict auto-resolution is a generic need. Keeping
the generic path first-class protects that optionality and keeps it honest
as a test surface; the Formidable layer is *additive*, not replacement.
Every Formidable-first capability is built on top of an already-shipped
generic counterpart (see §11).

---

## 2.6 Formidable-side opt-in (informational)

§2.5 describes the *server's* switches. The *client* has its own opt-in
hierarchy. This is informational — the server does not gate on it —
but worth documenting so future sessions don't conflate "is gigot
active on this server?" with "is this template being synced by the
client?".

- **App level** — never. There is no single "Formidable is
  gigot-enabled" switch, and there won't be one.
- **Profile level** — deferred. A profile-level boolean will come
  online once the gigot service described in this document exists in
  full; it does not exist in Formidable today (no `gigot_*` keys in
  `schemas/config.schema.js`).
- **Template level** — `gigot_enabled: bool` on the template YAML
  (Formidable `schemas/template.schema.js`). **Exists today** as a
  designer-side intent marker, surfaced in the template editor as a
  switch. Not yet wired to any sync behaviour on the client.
- **Record level** — `meta.gigot_enabled: bool` on every
  `storage/**/*.meta.json`, stamped from the governing template at
  create time (Formidable `modules/formActions.js`). **Exists today**,
  travels with the record.

**What this means for the server.** The server treats `gigot_enabled`
as opaque payload, but must merge and preserve it correctly (§10.2,
§10.7) because it is schema-load-bearing client data. The server
**does not** gate acceptance on `meta.gigot_enabled: false` while the
flag is still a forward-looking marker with no client-side
enforcement. Revisit once the Formidable profile-level toggle lands:
at that point, strict rejection of records whose template is not
sync-enabled becomes a reasonable invariant to check.

---

## 2.7 Marker provisioning on create/clone

§2.5 specifies that on a `formidable_first: true` server, a repo only
gets Formidable-aware behaviour if it carries
`.formidable/context.json`. This section specifies **how the marker
gets onto a repo in the first place**, so the server's mode is the
primary driver and per-request flags collapse into an override.

**Rule — Formidable-first server (`formidable_first: true`).**

- `POST /api/repos` with no `source_url` ("init") stamps the marker
  as part of the initial scaffold commit. Same payload as today's
  explicit `scaffold_formidable: true`.
- `POST /api/repos` with `source_url` ("clone") stamps the marker as
  a single commit on top of the cloned history, authored and
  committed by the scaffolder identity, containing only
  `.formidable/context.json`. Idempotent: if the cloned tree already
  carries a valid marker, no extra commit is written and the repo
  is left bit-for-bit equal to its upstream.

**Rule — generic server (`formidable_first: false`, default).**

Default stamping is off. `scaffold_formidable: true` is the explicit
per-request opt-in to stamp — on both init *and* clone. The
`source_url + scaffold_formidable: true` combination that today 400s
at `handler_repos.go:112` becomes valid: clone first, then stamp-if-
absent on top (identical plumbing to the Formidable-first clone path).
That subsumes the original Clone-as-Formidable task. Clones without
the flag are never auto-stamped.

**Per-request override.** `scaffold_formidable: false` on a
Formidable-first server suppresses stamping for *this* repo,
preserving the coexistence guarantee in §2.5 — "a Formidable-first
deployment can still host a plain structured-sync repo without the
server choking on non-conforming content." Reach for this when the
repo is a mirror of a plain upstream and a stamp commit would show up
on a later mirror push.

**Non-retroactive.** Flipping `formidable_first` from `false` to
`true` on a running server with existing repos does **not** migrate
them. Existing repos stay marker-less (and therefore generic-mode per
§2.5) until re-stamped by an explicit admin action — not yet
specified; probably a `POST /api/repos/{name}/formidable/stamp`
endpoint gated on admin session, sized when we need it. Silent
auto-migration is explicitly rejected: it would write commits the
operator didn't ask for.

**Mirror-sync interaction.** The stamp commit on a clone is authored
by the scaffolder and does not exist in the upstream. A later mirror
push back to that upstream (see the mirror-sync roadmap item) will
include it. Mirrors where the upstream must stay untouched want
`scaffold_formidable: false` at clone time; there is no way to
un-stamp after the fact short of a history rewrite, which the mirror
push would then reject on fast-forward grounds anyway.

**Why this framing over the original "Clone-as-Formidable" task.**
The earlier README item described the change narrowly — "lift the
mutually-exclusive validation on `source_url` + `scaffold_formidable:
true`". That phrasing put the decision on the *caller*: every
`POST /api/repos` has to remember to tick the box. On a deployment
dedicated to Formidable that's churn and a footgun (forget the flag
and you've imported a repo the F-phases will refuse to touch).
Moving the decision to server config matches the §2.5 framing of
Formidable-first as a deployment mode, and the per-request override
keeps the §2.5 escape hatch intact.

### 2.7.1 Implementation surface

- **Handler**: `handleCreateRepo` (`internal/server/handler_repos.go`)
  drops the `source_url && scaffold_formidable` 400 branch and instead
  resolves the effective stamp decision from
  `(cfg.formidable_first, req.source_url, req.scaffold_formidable)`:

  | `formidable_first` | `source_url` | `scaffold_formidable` | Effect |
  | ------------------ | ------------ | --------------------- | ------ |
  | `false`            | empty        | omitted               | empty init (today) |
  | `false`            | empty        | `true`                | init + scaffold (today) |
  | `false`            | empty        | `false`               | empty init |
  | `false`            | set          | omitted               | clone, no stamp (today) |
  | `false`            | set          | `true`                | clone + stamp-if-absent *(new — the original Clone-as-Formidable case)* |
  | `false`            | set          | `false`               | clone, no stamp |
  | `true`             | empty        | omitted               | init + scaffold *(server-default flip)* |
  | `true`             | empty        | `true`                | init + scaffold |
  | `true`             | empty        | `false`               | empty init (admin opt-out) |
  | `true`             | set          | omitted               | clone + stamp-if-absent *(server-default flip)* |
  | `true`             | set          | `true`                | clone + stamp-if-absent |
  | `true`             | set          | `false`               | clone, no stamp (mirror opt-out) |

  Boolean shape: `shouldStamp = explicit ?? cfg.formidable_first` where
  `explicit` is the request's tri-state flag. This requires changing
  `CreateRepoRequest.ScaffoldFormidable` from `bool` to `*bool` (or
  equivalent) so "omitted" can be distinguished from "explicit
  false" — the `bool` default of `false` conflates them today and is
  safe only because the server-default is also `false`. Once the
  server-default can be `true`, tri-state is required.

- **Clone-stamp primitive**: new `Manager.StampFormidableMarker(name)`
  in `internal/git`. Reads the current HEAD tree; if `.formidable/
  context.json` is present and parses cleanly, returns a
  `StampSkipped` sentinel the handler maps to "no extra commit".
  Otherwise builds a one-file tree on top of HEAD with a freshly
  generated marker (reuse `buildFormidableMarker` from
  `formidable_scaffold.go`) and writes it via the same plumbing used
  by `WriteFile` — author and committer both scaffolder, message
  `"Add Formidable context marker"`.

- **Config**: `internal/config` grows a single bool
  `server.formidable_first` (or similar nested key) plumbed through
  to `Server`. Default `false`. No runtime reload.

- **Tests**: the handler test `TestCreateRepoCloneAndScaffold
  MutuallyExclusive` gets replaced by a table-driven test walking
  the matrix above. A manager test for `StampFormidableMarker`
  covers the three cases: fresh clone without marker, clone that
  already has a marker (skip), empty repo (reject — nothing to
  commit on top of).

- **Docs**: README's "Clone-as-Formidable" roadmap item shrinks to a
  pointer at §2.7; Configuration Reference gains a
  `server.formidable_first` row.

---

## 3. API surface

All paths below are under `/api/repos/{name}/`. All bodies are JSON unless
noted. All sit under the existing sealed-body middleware — a Formidable
client that enrolled a pubkey gets E2E encryption for free. Bearer auth
(subscription key) is required; per-repo allowlist enforced as today.

### 3.1 Head pointer

```
GET /api/repos/{name}/head
→ 200 { "version": "<sha>", "default_branch": "main" }
```

Cheap, used by the client to detect "is there something new?" without pulling
a full tree.

### 3.2 Tree listing

```
GET /api/repos/{name}/tree?version=<sha>        # version optional, defaults to HEAD
→ 200 {
    "version": "<sha>",
    "files": [
      { "path": "templates/basic.yaml", "size": 312, "blob": "<sha>" },
      ...
    ]
  }
```

Lets the client diff against its own snapshot cheaply before pulling content.

### 3.3 Snapshot (initial populate + disaster recovery)

```
GET /api/repos/{name}/snapshot?version=<sha>    # version optional
→ 200 {
    "version": "<sha>",
    "files": [
      { "path": "...", "content_b64": "..." },
      ...
    ]
  }
```

Phase-1 format is JSON-with-base64 for simplicity. If payload size becomes a
problem, add `Accept: application/x-tar` later — don't pre-optimise.

### 3.4 Single-file read

```
GET /api/repos/{name}/files/{path}?version=<sha>
→ 200 { "version": "<sha>", "path": "...", "content_b64": "..." }
→ 404 if path not in that version
```

### 3.5 Single-file write (the core write path)

```
PUT /api/repos/{name}/files/{path}
{
  "parent_version": "<sha>",
  "content_b64": "...",
  "author": { "name": "Alice", "email": "alice@example.com" },
  "message": "Update basic template"
}

→ 200 { "version": "<new-sha>" }                     # fast-forward
→ 200 {                                              # auto-merged
    "version": "<new-sha>",
    "merged_from": "<parent_version>",
    "merged_with": "<prior-head>"
  }
→ 409 {
    "current_version": "<sha>",
    "path": "...",
    "conflict": {
      "base_b64":   "...",    # common ancestor blob (may be absent if add/add)
      "theirs_b64": "...",    # current HEAD blob (may be absent if delete)
      "yours_b64":  "..."     # the client's submission, echoed back
    }
  }
```

### 3.6 Multi-file atomic commit

```
POST /api/repos/{name}/commits
{
  "parent_version": "<sha>",
  "changes": [
    { "op": "put",    "path": "templates/a.yaml", "content_b64": "..." },
    { "op": "delete", "path": "templates/b.yaml" }
  ],
  "author":  { "name": "Alice", "email": "alice@example.com" },
  "message": "Rename a→c"
}

→ 200 { "version": "<new-sha>" }
→ 409 {
    "current_version": "<sha>",
    "conflicts": [
      { "path": "...", "base_b64": "...", "theirs_b64": "...", "yours_b64": "..." }
    ]
  }
```

### 3.7 Delta since version

```
GET /api/repos/{name}/changes?since=<sha>
→ 200 {
    "from": "<sha>", "to": "<sha>",
    "changes": [
      { "path": "...", "op": "added"   | "modified" | "deleted", "blob": "<sha>" }
    ]
  }
```

Lets the client pull only what changed between last sync and current HEAD.

### 3.8 (Later) Live notifications

Out of scope for the initial phases. Options when we get there: SSE on
`GET /api/repos/{name}/events`, or webhooks posted to a client-supplied URL.
Long-polling on `head` is the dumb fallback that works today.

---

## 4. Conflict semantics

We use `git merge-tree` (the modern, plumbing-friendly form available since
git 2.38) to perform 3-way merges without a worktree. Flow for `PUT`:

1. Load `parent_version` (client-supplied), `current_head`, and the blob at
   `path` in each.
2. If `parent_version == current_head`: fast-forward commit. Done.
3. If `parent_version` is an ancestor of `current_head`:
   - For the single changed path: 3-way merge base/theirs/yours.
   - If clean: write the merged blob, create a merge commit with two parents
     (`current_head`, plus a synthetic commit carrying just the client's
     change against `parent_version`). Return `merged_from` + `merged_with`.
   - If dirty: return `409` with the three blobs. Client decides what to do.
4. If `parent_version` is *not* an ancestor (client forked long ago, history
   rewritten, or outright garbage): return `409` with `current_version` and
   the client's own blob echoed back — no auto-merge attempted. Client must
   re-fetch snapshot and re-apply.

`POST /commits` extends this per-path and is transactional: if *any* path
conflicts, the whole commit is rejected. No partial apply.

### Formidable YAML-aware merge (later phase)

Line-based text merge is adequate for templates in the MVP. Once the
protocol is stable, Phase 5 can plug a structured YAML merger for the common
"two people added different fields to the same template" case that line-merge
would falsely flag as a conflict.

---

## 5. Coexistence with existing endpoints

| Surface                          | Who uses it                          | After this work                          |
| -------------------------------- | ------------------------------------ | ---------------------------------------- |
| `/git/{name}/*` smart-HTTP       | Power users, CI, external mirrors    | Stays. Unchanged.                        |
| `/api/repos` (list/create/delete) | Admin UI, automation                 | Stays. Unchanged.                        |
| `/api/repos/{name}/status`       | Admin UI                             | Stays. Unchanged.                        |
| `/api/repos/{name}/branches|log` | Admin UI                             | Stays. Unchanged.                        |
| `/api/repos/{name}/head|tree|snapshot|files|commits|changes` | Formidable client | **New**, this doc. |

The sealed-body middleware already covers `/api/*`, so new endpoints inherit
E2E encryption automatically.

On a `formidable_first` server with the marker file present in a repo, the
same `/api/repos/{name}/*` endpoints gain Formidable-aware behaviour
(schema-driven merges, referential checks, a record-query endpoint — see
§10). The wire format is unchanged; only the server's interpretation of
payloads and conflict shape changes.

---

## 6. Execution phases

Each phase is independently shippable — the client can adopt them
incrementally, and the server is usable between phases. Do not collapse
phases; shipping one cleanly before starting the next is the point.

### Phase 0 — Groundwork & naming

**Goal:** settle the pieces that all later phases depend on.

Scope:
- **Marker file — accepted & wired 2026-04-16.** `.formidable/context.json`
  with `{ "version": 1, "scaffolded_by": "gigot", "scaffolded_at":
  "<RFC3339>" }`. Written by the scaffolder at repo-creation time when
  `scaffold_formidable: true`
  (`internal/server/formidable_scaffold.go:buildFormidableMarker`); read
  by a `formidable_first: true` server to decide whether to apply the
  schema-aware behaviour of §10 to a given repo. Existing repos
  scaffolded before this wiring landed do not have the marker; they can
  be stamped manually or re-scaffolded.
- **Handler package — settled.** New sync handlers live in
  `internal/server/handler_sync.go` alongside existing handlers; DTOs in
  `models_sync.go`. Flat routes, no sub-router.
- **Merge engine — accepted 2026-04-16: shell out to `git merge-tree`.**
  Matches the existing style of `internal/git/manager.go` (every git
  operation is already a subprocess), assumes git ≥ 2.38 on the service
  host which is fine for our deployment targets, and avoids pulling in
  `go-git` — whose merge support has historically lagged real git and
  would be a large new dependency for one feature. Command shape will be
  confirmed when Phase 2 implements the wrapper.

Acceptance: all three decisions recorded ✓; marker file embedded in the
scaffold payload and covered by `TestFormidableScaffoldFiles_ExpectedPayload`
plus the `scaffold.feature` integration scenario. Phase 0 closed.

### Phase 1 — Read path (head, tree, snapshot, single-file read)

**Goal:** a Formidable client can populate itself from a GiGot repo without
ever speaking git.

Scope:
- `GET /head`, `GET /tree`, `GET /snapshot`, `GET /files/{path}`.
- Cover each with a unit test (`handler_sync_test.go`) and at least one
  feature file under `integration/features/`.
- Swagger annotations (`swag init` regenerates `docs/`).

Acceptance: `curl` walkthroughs from README pass against a fresh repo
scaffolded with `scaffold_formidable: true`.

**Shipped 2026-04-16.** All four endpoints live in
`internal/server/handler_sync.go`; manager methods (`Head`, `Tree`,
`Snapshot`, `File`) live in `internal/git/manager.go` with sentinel errors
`ErrRepoEmpty`, `ErrVersionNotFound`, `ErrPathNotFound` mapped centrally by
`writeSyncError`. Unit tests, handler tests, and `integration/features/sync.feature`
scenarios cover 404/409/422/200-with-content paths for every endpoint.

### Phase 2 — Single-file write path

**Goal:** a Formidable client can edit one template at a time and never see
a non-fast-forward.

Scope:
- `PUT /files/{path}` with fast-forward, auto-merge, and conflict paths.
- Implement the `git merge-tree` wrapper. Unit tests for:
  - fast-forward
  - clean auto-merge (different files modified by client vs server)
  - clean auto-merge (same file, non-overlapping lines)
  - dirty conflict (same file, overlapping lines) → 409 shape
  - ancestor-not-found → 409 with echoed content
- Enforce per-repo allowlist (existing `policy.TokenRepoPolicy`).
- Record subscription-key username in the commit trailer regardless of
  client-supplied author, so audit survives a compromised/forged client.

Acceptance: a scripted concurrency test (two clients, same file, non-overlapping
edits) produces a single merge commit with two parents and no data loss.

**Shipped 2026-04-17.** Write primitive lives in
`internal/git/write.go:WriteFile`; plumbing uses `git hash-object`,
`read-tree`/`update-index`/`write-tree` against a temp `GIT_INDEX_FILE`,
`commit-tree`, and `git merge-tree --write-tree` for auto-merge. Ref
updates are CAS via `git update-ref <next> <expect>` so concurrent writers
race safely. Handler is `handleRepoFilePut` in
`internal/server/handler_sync.go`; 409 conflict body matches §3.5 and
`ErrStaleParent` is surfaced as an ancestor-not-found 409 with only
`yours_b64` populated. Author defaults to the authenticated identity's
username (`<username>@gigot.local`) when the client omits an author block;
committer is always the scaffolder identity so `git log` keeps the
server's role auditable. Unit tests cover the five scenarios listed above
plus bad-parent/empty-repo/missing-repo sentinels; handler tests cover
the HTTP surface; `integration/features/sync.feature` has seven PUT
scenarios (404/409/400×2/422/200 fast-forward).

### Phase 3 — Multi-file atomic commits

**Goal:** Formidable can rename/move/bulk-edit in one shot.

Scope:
- `POST /commits` with put/delete ops, transactional behaviour, conflict
  response listing all failing paths.
- Shared merge engine with Phase 2 — this is a loop over Phase 2's logic plus
  an all-or-nothing commit.

Acceptance: rename test (delete A + put B in one request) produces exactly
one commit; conflict on any path aborts the whole commit.

**Shipped 2026-04-17.** Multi-file primitive lives in
`internal/git/commit.go:Commit`; handler is `handleRepoCommits` in
`internal/server/handler_sync.go`. Reuses Phase 2's low-level helpers
(`hashObject`, `commitTree`, `mergeTree`, `updateRefCAS`) plus a new
`treeWithChanges` that applies an ordered put/delete sequence against a
throwaway GIT_INDEX_FILE (with GIT_WORK_TREE set — bare repos refuse
`update-index --force-remove` without one). Merge-tree now runs with
`--name-only --no-messages` so conflict paths parse cleanly for the
multi-path 409 body. `CommitConflictError` carries `[]WriteConflict`,
one entry per failing path; on stale-parent the list echoes every client
change with only `yours_b64` populated (delete ops leave `yours_b64`
empty). Unit tests cover rename-as-fast-forward, auto-merge across
disjoint files, transactional abort on conflict, stale-parent, and the
standard validation + sentinel paths; handler tests mirror the surface;
`integration/features/sync.feature` has nine `/commits` scenarios.

### Phase 4 — Delta / efficient pull

**Goal:** keep sync cost low as repos grow.

Scope:
- `GET /changes?since=<sha>` returning added/modified/deleted lists.
- Tree diff implemented in terms of git's own diff plumbing.

Acceptance: a client that's one commit behind pulls only the changed blobs,
not the whole snapshot.

**Shipped 2026-04-17.** Delta primitive lives in
`internal/git/changes.go:Changes`; handler is `handleRepoChanges` in
`internal/server/handler_sync.go`. Uses `git diff-tree --raw -r -z
--no-commit-id <since> HEAD`; `-z` gives NUL-separated records so paths
with spaces or newlines parse unambiguously. `since` must be a strict
ancestor of HEAD (or equal) — non-ancestor surfaces as `ErrStaleParent`
and maps to 409, forcing the client to re-snapshot rather than consume
a misleading diff. `since == HEAD` returns an empty `changes[]` list
without running diff-tree. Added/modified entries carry the new blob
SHA; deleted entries carry the pre-change blob SHA so a client always
has something concrete to fetch. Rename detection (`-M`) is *off* —
renames surface as delete+add, mirroring Phase 3's own transactional
model. Unit tests cover add/modify/delete mix, no-op, missing/empty
repo, bad/missing since, stale since, and nested/spaced paths; handler
tests mirror the HTTP surface;
`integration/features/sync.feature` adds eight `/changes` scenarios
(404/409/400/422/stale-409/no-op-200/added-200/405).

### Phase 5 — Polish & Formidable-aware merging

Optional, driven by real usage. Candidates:
- YAML-structured merge for `templates/*.yaml` (reduces false conflicts).
- Advisory per-template locks as a UX hint (shown in Formidable as "X is
  editing"). Not enforced as auth; purely cooperative.
- Live updates (SSE on `/events` or webhook dispatch).

Do not pre-build these. Revisit after Phase 4 ships and there's usage data.

---

## 7. Open questions (cross-phase)

- **Binary files in generic mode.** `storage/` may contain attachments.
  Base64 in JSON is fine up to ~a few MB. If a generic-mode client stores
  large binaries, we need a streaming path (`multipart/form-data` or raw
  body) before Phase 1 declares done. In Formidable-first mode the picture
  is sharper — images live under `storage/<template>/images/` and are
  referenced by `type: image` fields; Phase F3 defines the upload path and
  referential-integrity rules, which may also be the point where we settle
  the generic streaming story. Decide early; retrofitting later churns the
  client.
- **Branch model.** MVP assumes the repo has a single writable branch
  (`default_branch`, typically `main`). Multi-branch workflows are out of
  scope — call this out in the README so nobody ships a feature that depends
  on it.
- **Rate limiting / write bursts.** Nothing today. Acceptable for MVP;
  revisit if we see clients hammering `PUT` in autosave loops.
- **Author trust.** We echo client-supplied `author.name/email` in commits.
  This is fine for attribution but must never be used for authorisation
  decisions — those key off the subscription-key's bound username.
- **Admin UI integration.** Once Phase 2 lands, the admin UI can show a
  "recent edits" view per repo. Low priority, call it out to avoid forgetting.

---

## 8. Out of scope (explicitly)

- Real-time collaborative editing (OT/CRDT). Formidable is local-first; conflicts
  are resolved on sync, not live.
- Rewriting history (rebase, force-push, squash). Not exposed. Power users
  can still do it via the smart-HTTP endpoint if they must.
- Per-file locking as a hard constraint. Phase 5 may add cooperative locks;
  hard locking is explicitly not happening.
- Replacing the smart-HTTP endpoints. They stay. This is additive.
- Scaling GiGot as a multi-tenant generic structured-sync backend for
  non-Formidable clients. Technically possible — the generic mode is
  first-class — but not the design target. Formidable is the primary
  customer, and capacity/roadmap decisions resolve in its favour when
  they conflict.

---

## 9. How to use this document

Future Claude sessions picking up sync work:

1. Read this file top to bottom.
2. Check `README.md` Roadmap to see which phase is current.
3. Work one phase at a time. Do not scope-creep into the next phase — the
   phase boundaries are deliberate; shipping Phase 1 before Phase 2 is a
   feature.
4. When a phase lands, update both the Roadmap (move the item into the
   "Done and shipping" block) and this document's status line. If a design
   choice here turns out wrong mid-phase, **update this doc before writing
   more code** so the next session isn't working from a stale plan.
5. Formidable-first phases (F1–F4, §11) layer on top of their generic
   counterparts. A server running generic Phase N + Formidable Phase F(N-1)
   is a supported configuration. Do not start an F-phase before its
   underlying generic phase has shipped — the generic path is the fallback
   and the test surface.

---

## 10. Formidable-first mode

Activated when `formidable_first: true` is set server-side **and**
`.formidable/context.json` is present in the target repo. The protocol
endpoints from §3 stay unchanged; the server's behaviour on top of them
changes.

### 10.1 What the server parses

On every write (and lazily on reads that benefit from it), the server
parses:

- `templates/*.yaml` — form definitions. Relevant keys: `filename`,
  `fields` (ordered list of typed field defs), `item_field`,
  `enable_collection`, `markdown_template`, `sidebar_expression`.
- `storage/<template>/*.meta.json` — records with shape
  `{ meta: {...}, data: {...} }`. `meta.template` names the governing
  template filename.

Field types come from Formidable's known set: `text`, `textarea`,
`boolean`, `number`, `range`, `date`, `dropdown`, `radio`, `multioption`,
`list`, `table`, `image`, `link`, `tags`, `guid`, `code`, `latex`, `api`,
plus `loopstart`/`loopstop` as structural brackets. Unknown field types
are treated as opaque text — forward-compatibility by default.

### 10.2 Record merge semantics

When both client and server have modified the same
`storage/**/*.meta.json` since `parent_version`, the server merges per
JSON key instead of per line. Rules, top to bottom:

- **`meta.updated`** — pick `max(updated_theirs, updated_yours)`. This
  field regenerates on every save; line-based merge would conflict here
  *every time*, defeating the whole design. Explicitly never a conflict.
- **`meta.tags`** — set union over the normalised (lowercased, trimmed)
  values, re-sorted. Matches Formidable's own `sanitize` behaviour.
- **`meta.flagged`** — if divergent, prefer `true`. Flagging is loud;
  unflagging is cheap to redo.
- **`meta.created`, `meta.id`, `meta.template`** — must not change after
  creation. If either side has altered them, return 409 — this is a
  corrupt client, not a real conflict.
- **`meta.author_name`, `meta.author_email`** — follow the winning
  `meta.updated` (last-writer-wins on attribution).
- **`meta.gigot_enabled`** — follow the winning `meta.updated`.
  (F2 may replace this with a cross-file re-stamp from the governing
  template.)
- **`data.<field-key>`** — opaque value, merged by the uniform rule in
  §10.3. No per-type logic.

If every key resolves without conflict, the server produces a single
merge commit whose tree is the merged JSON, re-serialised canonically
(sorted keys throughout; array positions preserved as-is).

### 10.3 Uniform field resolution

Every `data.*` key, regardless of declared field type, resolves by a
single rule:

- Neither side changed the value (both equal `base`) → keep `base`.
- One side changed, the other didn't → take the changed side.
- Both sides changed to the same value → no-op.
- Both sides changed to different values → take the side whose
  `meta.updated` is newer (last-writer-wins).

Equality is deep-equality on the decoded JSON value, so a `textarea`
string, a `list` array, and a `number` are all treated the same: the
field's whole value is atomic.

Rationale: clients already ship the whole field value on every save
(they don't PATCH sub-structure), so treating each field as atomic
matches the wire contract and removes an entire class of merge
conflicts that the user would have had to resolve by hand. Per-type
cleverness (line-merging inside `textarea`, set-union on `multioption`,
row-keying on `table`) is expressly not in scope — the simple rule
handles every type.

The one exception is `image` fields: the referenced filename merges by
the uniform rule, but the referenced blob in `images/` is subject to
referential integrity in F3 (§10.5). F1 just takes the later filename
and lets F3 worry about dangling references.

### 10.4 Schema validation on commit

**Descoped.** Server-side schema validation would couple GiGot to
Formidable's field-type model; that coupling is explicitly rejected.
The template is Formidable's contract with the client, not with the
server. Corrupt records are caught at render time on the client, not
at commit time on the server. The uniform rule in §10.3 treats
unknown `data.*` keys the same as known ones, so extraneous keys
survive a merge harmlessly. §10.5 follows the same principle — image
referential integrity stays on the client.

### 10.5 Referential integrity for image fields

**Descoped.** Whether a record references an image that exists — or
whether an image on disk is still referenced — is Formidable's problem,
not GiGot's. Checking it on the server would require exactly the
template coupling rejected in §10.4. The server treats image blobs as
ordinary binary files under `storage/<template>/images/`: it transports
them (§11 F3) and leaves orphaned files / dangling references for the
client to handle. The only conflict GiGot resolves is the record one
(§10.3); two people overwriting the same image file isn't a realistic
scenario worth a dedicated rule.

### 10.6 Per-field conflict shape

With the uniform rule in §10.3, `data.*` fields never produce 409s —
last-writer-wins resolves them. Conflicts only arise when a client
attempts to mutate one of the immutable `meta.*` keys (`created`,
`id`, `template`). The 409 body calls those out explicitly:

```
409 {
  "current_version": "<sha>",
  "path": "storage/addresses/baker-residence.meta.json",
  "field_conflicts": [
    {
      "scope": "meta",
      "key":   "created",
      "reason": "immutable"
    }
  ]
}
```

Multiple immutable violations on the same record are bundled into one
`field_conflicts` array. The client surfaces this as "corrupt client,
your save was rejected" — not a user-resolvable merge dialog.

### 10.7 Template writes

**Descoped.** Templates are YAML files. The generic Phase 2/3 line-based
merge handles them; no Formidable-aware code path. Two designers
adding fields on stale parents will 409 at the YAML line level — rare
enough in practice that the structural-merge cost isn't justified, and
building it would re-introduce the coupling we're avoiding in §10.4.

### 10.8 Record query endpoint

Layered on top of Phase 4 (`GET /changes`):

```
GET /api/repos/{name}/records/{template}?where=<expr>&sort=<field>&limit=<n>
→ 200 { "version": "<sha>", "records": [ { ... full meta.json ... } ] }
```

`<expr>` is a small filter DSL over `data.*` keys (MVP: equality and
simple range on scalar fields — `where=city=London`, `where=range>5`).
Larger query needs can come later.

The server maintains an in-memory index per repo, invalidated on
commit. Cold-start cost is proportional to `storage/**/*.meta.json`
count, amortised by the parse the server already does for merges.

### 10.9 What stays out

- **Live updates** — still the Phase 5 / §3.8 story, out of initial
  scope in both modes. SSE via a small Go library (e.g. `r3labs/sse`)
  is the likely primitive when we get there; long-polling on `/head`
  is the dumb fallback that works today.
- **Cross-repo queries** — not planned.
- **Full-text search / JMESPath** — overkill for MVP query needs; add
  only if usage demands.
- **Live presence / co-editing** — a separate future service (SignalR-style)
  will show which fields peers are editing and push in-place updates,
  so two users hardly ever race on the same field. That service makes
  §10.3's last-writer-wins an acceptable *last-resort* tie-break rather
  than the common case. Not in scope here; called out so the simplicity
  of §10.3 is understood as deliberate.

---

## 11. Formidable-first execution phases

Each phase layers on top of a generic phase — the generic behaviour
ships first, the Formidable behaviour is an upgrade. A server can run
at any combination (e.g. generic Phase 2 only, or Phase 2 + F1, or
Phase 4 + F1 + F2 without F3/F4).

### Phase F1 — Record merge (uniform field rule)

**Layers on:** Phase 2 (generic single-file write).

Scope:
- Record JSON parser (`{ meta, data }`).
- `meta.*` rules from §10.2 (updated-max, tags set-union, flagged
  true-wins, immutable created/id/template, others follow updated
  winner).
- Uniform `data.*` rule from §10.3: one-side-changed → take changed
  side; both-same → no-op; both-different → last-writer-wins by
  `meta.updated`. No per-type dispatch.
- Canonical JSON output for deterministic merge bytes.
- 409 response shape from §10.6, used only for immutable-meta violations.
- Wiring into `PUT /files/{path}` and `POST /commits` for paths matching
  `storage/**/*.meta.json` on repos with a Formidable marker.

Explicitly **not** in scope:
- Template parsing, schema validation, strictness knobs — all F2.
- Image referential integrity — F3.

Acceptance:
- Two clients edit different fields on the same record with stale
  parents ⇒ auto-merge succeeds, one merge commit, no human
  intervention.
- Two clients edit the same data field to different values ⇒ the client
  whose `meta.updated` is newer wins, 200 OK, no 409.
- `updated` never triggers a conflict.
- A client that mutates `meta.created`, `meta.id`, or `meta.template`
  gets 409 with the field named.
- Merge output bytes are stable across servers (canonical JSON).

### Phase F2 — Descoped (2026-04-18)

F2 originally covered template structural merge and server-side
schema validation. Both are out:

- **Schema validation** would couple GiGot to Formidable's field-type
  model. The template is the client's contract, not the server's —
  corrupt records are caught at render time, not commit time. See
  §10.4.
- **Template structural merge** (§10.7) is a nice-to-have that the
  generic line-based merge already covers well enough. Templates are
  edited rarely; rebuilding this phase only to avoid occasional YAML
  merge friction isn't worth the code it would take.

Phase number retained so F3/F4 numbering stays stable. Reopen only if
production usage shows template line-merge causing real pain.

### Phase F3 — Binary transport for images

**Layers on:** Phase 3 (multi-file commits).

Images land as ordinary binary blobs under
`storage/<template>/images/`. The only merge path that can conflict is
the record (`*.meta.json`), which §10.3 already covers. No
referential integrity, no template parsing.

### Phase F4 — Record query endpoint

**Layers on:** Phase 4 (generic `/changes`).

Scope:
- In-memory record index per repo, invalidated on commit.
- `GET /records/{template}` with the minimal filter DSL in §10.8.
- Index rebuild on server restart; no persistence (rebuild cost is
  cheap for realistic repo sizes — benchmark during phase).

Acceptance:
- Listing all records of a template returns them in ≤100ms for 10k
  records on a dev machine.
- Equality filter on a scalar field returns correct subset.
- Commit invalidates and rebuilds the affected template's slice only.
