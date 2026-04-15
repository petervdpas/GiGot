# Structured Sync API — Design & Execution Plan

**Status:** accepted, not yet implemented
**Owner:** Peter
**Last updated:** 2026-04-15

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

---

## 6. Execution phases

Each phase is independently shippable — the client can adopt them
incrementally, and the server is usable between phases. Do not collapse
phases; shipping one cleanly before starting the next is the point.

### Phase 0 — Groundwork & naming

**Goal:** settle the pieces that all later phases depend on.

Scope:
- Decide the Formidable context marker file (the deferred roadmap task). The
  marker lives inside the scaffold and is what lets the client recognise a
  gigot-managed context. Recommendation (not yet accepted): `.formidable/context.json`
  with `{ "version": 1, "scaffolded_by": "gigot", "scaffolded_at": "<RFC3339>" }`.
  Resolve this *before* the client starts reading it.
- Pick a single internal package for the new handlers (proposal:
  `internal/server/handler_sync.go` alongside existing handlers; DTOs in
  `models_sync.go`). Avoid a sub-router if flat routes read cleanly.
- Confirm the `git merge-tree` command shape we'll shell out to (or decide
  whether to use a Go library like `go-git` for merges — spike both before
  committing).

Acceptance: marker file decided + one test repo scaffolded with it; choice
between shell-out vs go-git written up here with the reasoning.

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

### Phase 3 — Multi-file atomic commits

**Goal:** Formidable can rename/move/bulk-edit in one shot.

Scope:
- `POST /commits` with put/delete ops, transactional behaviour, conflict
  response listing all failing paths.
- Shared merge engine with Phase 2 — this is a loop over Phase 2's logic plus
  an all-or-nothing commit.

Acceptance: rename test (delete A + put B in one request) produces exactly
one commit; conflict on any path aborts the whole commit.

### Phase 4 — Delta / efficient pull

**Goal:** keep sync cost low as repos grow.

Scope:
- `GET /changes?since=<sha>` returning added/modified/deleted lists.
- Tree diff implemented in terms of git's own diff plumbing.

Acceptance: a client that's one commit behind pulls only the changed blobs,
not the whole snapshot.

### Phase 5 — Polish & Formidable-aware merging

Optional, driven by real usage. Candidates:
- YAML-structured merge for `templates/*.yaml` (reduces false conflicts).
- Advisory per-template locks as a UX hint (shown in Formidable as "X is
  editing"). Not enforced as auth; purely cooperative.
- Live updates (SSE on `/events` or webhook dispatch).

Do not pre-build these. Revisit after Phase 4 ships and there's usage data.

---

## 7. Open questions (cross-phase)

- **Binary files.** `storage/` may contain attachments. Base64 in JSON is fine
  up to ~a few MB. If Formidable stores large binaries, we need a streaming
  path (`multipart/form-data` or raw body) before Phase 1 declares done.
  Decide early; retrofitting later churns the client.
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
