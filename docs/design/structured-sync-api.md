# Structured Sync API — Design & Execution Plan

**Status:** accepted, not yet implemented. Formidable-first layer (§10–§11) added 2026-04-16.
**Owner:** Peter
**Last updated:** 2026-04-16

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
- **`data.<field-key>`** — resolved per field type (§10.3).

If every key resolves without conflict, the server produces a single
merge commit whose tree is the merged JSON, re-serialised canonically
(sorted keys for maps the schema doesn't order; preserved order for
arrays that are positional, like `table` rows).

### 10.3 Type-aware field resolution

Per `data.*` key, the server consults the template's `fields[]` entry
and applies:

| Field type | Merge rule |
| ---------- | ---------- |
| `text`, `number`, `boolean`, `date`, `range`, `dropdown`, `radio`, `guid`, `link` | If one side unchanged, take the changed side. If both changed to the same value, no-op. Otherwise 409. |
| `textarea` (`plain` or `markdown`) | Line-based 3-way merge — this is prose. Clean ⇒ ok; dirty ⇒ 409 with blob-triple under this field. |
| `latex`, `code` | Line-based 3-way merge, same as `textarea`. |
| `tags` | Set union (same rule as `meta.tags`). |
| `multioption` | Set union on selected values. |
| `list` | Element-level merge if the list has a `primary_key` field declared; otherwise line-based over canonical-serialised list. |
| `table` | Row-level merge keyed by the row's `primary_key` column if any; otherwise by row index (positional). Cell conflicts within a row bubble up as field-level 409s on that cell. |
| `image` | Last-writer-wins on `meta.updated`. The referenced filename is just a string; the actual blob lives in `images/` and is merged separately (§10.5). |
| `api` | Treat the cached value as opaque JSON; deep-merge objects, conflict on scalar disagreement. |
| Unknown type | Fall back to line-based merge. |

Loop groups (`loopstart`/`loopstop`) merge per iteration, keyed by the
loop entry's `guid` field if present, otherwise positional.

### 10.4 Schema validation on commit

Before writing any record, the server checks:

1. `meta.template` names an existing `templates/<name>.yaml` in the
   target version. Missing ⇒ 422.
2. Every `data.*` key appears in the template's `fields[]`. Extraneous
   keys ⇒ 422 by default; can be downgraded to a warning via config for
   forward-compat during template edits (open question — decide in F2).
3. Each field's value matches its declared type's coarse shape (string
   for text, array for list/table, ISO8601 for date, etc.). Mismatch ⇒
   422.

This catches corrupt clients and cross-file rename drift (template
renames a field, record still uses old key) at commit time rather than
at render time.

### 10.5 Referential integrity for image fields

For every `data.*` whose template field has `type: image`, the value is
expected to be a path relative to `storage/<template>/images/`. On
commit, the server checks:

- If a record references an image, that image exists in the target tree
  (either unchanged from the parent, or added in this same commit).
  Missing ⇒ 422.
- On merge, if one side deletes an image the other side still
  references, the deletion is held back — a 409 is returned naming the
  dangling reference, so the client can decide (drop the reference, or
  restore the image).

This is the only merge rule that crosses file boundaries. It's cheap:
the server already has both trees parsed for record merging.

### 10.6 Per-field conflict shape

When a record merge hits a real conflict, the 409 response is richer
than §3.5's blob-triple:

```
409 {
  "current_version": "<sha>",
  "path": "storage/addresses/baker-residence.meta.json",
  "record_id": "b8498b24-...",
  "template": "addresses.yaml",
  "field_conflicts": [
    {
      "key": "postal",
      "field_type": "text",
      "base":   "NW1 6XE",
      "theirs": "NW1 6XF",
      "yours":  "NW1 6XG"
    }
  ],
  "auto_merged_fields": ["city", "owners"]
}
```

Line-based conflicts inside `textarea`/`latex`/`code` fields keep the
blob-triple shape nested under the field entry. The client renders a
field-specific dialog, not a whole-file diff.

### 10.7 Template writes

Templates are YAML. The server merges:

- Top-level scalar keys (`name`, `item_field`, `enable_collection`,
  `sidebar_expression`) — last-writer-wins at commit time if both
  changed.
- `markdown_template` — line-based merge. It's Handlebars prose; line
  merge is the right tool. Conflict ⇒ 409 with blob-triple for this key
  only.
- `fields[]` — structural merge keyed by field `key`. Additions from
  both sides interleave in commit-time order. A removal from one side
  plus an edit on the other on the same key ⇒ 409. Reorderings are a
  no-op for keys both sides kept; if both sides reordered differently,
  409.

Field *rename* (same semantics, different `key`) is not auto-detected.
A rename on one side plus an edit on the other side will 409 as
"delete-vs-edit" — mirrors git's own behaviour on file renames.

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

---

## 11. Formidable-first execution phases

Each phase layers on top of a generic phase — the generic behaviour
ships first, the Formidable behaviour is an upgrade. A server can run
at any combination (e.g. generic Phase 2 only, or Phase 2 + F1, or
Phase 4 + F1 + F2 without F3/F4).

### Phase F1 — Schema parsing + per-field record merge

**Layers on:** Phase 2 (generic single-file write).

Scope:
- YAML template parser (reuse an existing Go YAML lib; no custom
  parser).
- Record JSON parser + validator keyed by `meta.template`.
- Field-level merge engine covering all types in §10.3.
- Per-field 409 response shape (§10.6).
- Configurable strictness for extraneous `data` keys (warn vs reject).

Acceptance:
- Two clients edit different fields on the same record with stale
  parents ⇒ auto-merge succeeds, one merge commit, no human
  intervention.
- Two clients edit the same scalar field ⇒ 409 naming the field only.
- `updated` never triggers a conflict.
- Corrupt record (missing template, wrong type) is rejected at commit.

### Phase F2 — Template writes + validation at commit

**Layers on:** Phase 2/3 (generic single-file + multi-file writes).

Scope:
- Structural `fields[]` merge for templates (§10.7).
- Cross-file check on commit: if a template is edited and records of
  that template are also touched in the same commit, verify record
  `data.*` keys still match the new `fields[]`.
- Warn (not reject) if an existing record elsewhere in the repo is now
  invalid after a template edit — surface this in the commit response
  so the client can prompt a migration flow.

Acceptance:
- Two template designers add different fields to the same template ⇒
  auto-merge with both fields present, preserving order.
- Template rename of a field key plus a record edit using the old key
  ⇒ 409 with a clear "template/record drift" message.

### Phase F3 — Referential integrity for image fields

**Layers on:** Phase 3 (multi-file commits).

Scope:
- On commit: parse records for `type: image` values, check referents
  exist.
- On merge: hold back image deletions that would orphan references;
  409 with named dangling references.
- Define the image-upload path — either `POST /commits` with binary
  blobs base64'd in the `changes[]` entry (MVP), or a streaming path
  (`multipart/form-data`) if blob sizes warrant it. Decide at phase
  start; this is where §7's "binary files" open question resolves for
  both modes.

Acceptance:
- Commit that deletes an image still referenced by a record is
  rejected.
- Merge that would orphan an image returns 409 with the offending
  record and field named.

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
