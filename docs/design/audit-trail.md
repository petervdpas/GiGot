# Audit Trail (`refs/audit/main`)

## Purpose

Every repo GiGot serves carries a server-authored audit chain at
`refs/audit/main`. One commit per audited operation, chained on the
previous entry by git's native parent link. The chain is:

- **Tamper-evident.** Git's object model hashes each commit (including its
  parent pointer), so any edit to a historical entry changes every SHA
  from that point forward.
- **Append-only.** GiGot is the only writer. Client pushes to anything
  under `refs/audit/*` are refused at the git protocol layer (see
  "Guardrails" below — currently deferred to slice 2).
- **Client-readable.** Formidable clients and any other consumer fetch
  the ref with `git fetch refs/audit/main:refs/audit/main`, then walk the
  log. No new HTTP surface.
- **Mirror-travelling.** The push worker (mirror-sync slice 2) will
  include `refs/audit/*` in its refspec, so a downstream mirror receives
  the audit chain alongside `refs/heads/*`.
- **Per-repo, every repo.** The chain is not a Formidable-first feature —
  it exists on generic repos too, as a GiGot provenance signature. A
  cloned-elsewhere repo still carries the chain it accumulated while
  GiGot hosted it.

## On-disk shape

Each audit entry is a regular git commit on `refs/audit/main`:

- **Tree.** Exactly one entry: `event.json` (regular file, mode 100644).
- **Parent.** The previous audit head. First entry has no parents.
- **Author/Committer.** Always `GiGot Audit <audit@gigot.local>`,
  regardless of which actor caused the underlying event. The actor goes
  inside the JSON payload — having a single signing identity on the
  chain makes verification trivial.
- **Commit message.** `audit: <type>` (or `audit: <type> (<notes>)`).
  The real payload is in `event.json`; the message is what `git log`
  prints.

## Event schema (`event.json`)

```json
{
  "time":  "2026-04-19T10:15:23.112Z",
  "type":  "repo_create",
  "actor": {
    "id":       "token-abcd1234",
    "username": "alice",
    "provider": "session"
  },
  "ref":   "refs/heads/main",
  "sha":   "a1b2c3d4...",
  "notes": "repository audit-create created"
}
```

All fields except `time` and `type` are optional. `time` is stamped by
GiGot if the caller leaves it zero. `type` is the discriminator every
reader branches on, so it is required.

### Initial event types

| Type            | Emitted on                                              |
| --------------- | ------------------------------------------------------- |
| `repo_create`   | `POST /api/repos` success (both init and clone paths)   |
| `file_put`      | `PUT /api/repos/{name}/files/{path}` success            |
| `commit`        | `POST /api/repos/{name}/commits` success                |
| `push_received` | *(deferred to slice 2)* `git-receive-pack` success      |

New types can be added without schema migration — unknown types are
ignorable by older readers.

## Writer contract

`internal/git.Manager.AppendAudit(repoName, AuditEvent)` is the sole
write path. It:

1. Hashes `event.json` as a blob (`git hash-object -w`).
2. Builds a one-file tree (`git mktree`).
3. Creates a commit chained on the current `refs/audit/main` (no parent
   if the ref does not exist yet).
4. Updates `refs/audit/main` via CAS (`git update-ref <ref> <new>
   <expect>`), retrying on contention up to 5 times.

Failures in the writer are logged but never surfaced to the user's
request — audit is observability for an operation that already took its
user-facing write. Dropping an entry is worth a log line, not a 500.

## Reader contract

Clients consume the chain with plain git:

```bash
git fetch origin 'refs/audit/main:refs/audit/main'
git log refs/audit/main --format=%H | while read sha; do
  git show "$sha:event.json"
done
```

Chain integrity is proved by walking `git rev-list refs/audit/main` —
any broken parent link or rewritten entry changes every downstream SHA,
which a client can detect by remembering the previous head between
fetches.

## Guardrails

### Tamper-proof (shipped — slice 2)

Every bare repo carries a `hooks/pre-receive` that refuses any ref update
whose refname starts with `refs/audit/`. The hook is installed by
`Manager.InitBare` / `Manager.CloneBare` on new repos and retro-installed
by `Manager.EnsureAuditGuards()` at server start on repos that predate
the guard. The hook body is the canonical text stored in
`internal/git.auditGuardHook`; re-running the installer overwrites any
hand-edited version because the guard is load-bearing.

`AppendAudit` uses `git update-ref` directly, which bypasses hooks by
design in git's plumbing layer. So the hook blocks client pushes via
`git-receive-pack` without interfering with server-side writes.

**Combined with slice 1's hash chain, the audit trail is now both
tamper-proof (cannot be overwritten by a client) and tamper-evident
(any out-of-band modification changes every downstream SHA).**

### Still outstanding (slice 3)

Emit `push_received` audit entries for the `git-receive-pack` path so
CLI `git push` operations are instrumented. Approach: snapshot
`git for-each-ref` before and after the receive-pack subprocess runs,
diff the two, and append one entry per changed ref. Tracked in the
README roadmap.

## Open questions

- **Mirror refspec.** Mirror-sync slice 2 must include `refs/audit/*` in
  its push refspec so downstream mirrors receive the chain. One-line
  change there; flagged here so it is not forgotten.
- **Retention.** Unbounded growth is acceptable short-term — the chain
  is one commit per audited op, payload is tens of bytes. If a repo
  accumulates millions of ops we will want a compaction strategy
  (e.g. archive-and-truncate at SHA checkpoints), but that is not a
  v1 concern.
- **Reader UX in Formidable.** The admin UI could render a tail of the
  chain per repo on the card view. Out of scope for this slice — the
  primitive is the chain, not the visualization.
