# F1 — Formidable-first record merge (prep)

**Status:** prep, not yet implemented. Written 2026-04-18 so the next session can pick up cold.

This document pins F1's scope, package layout, public API, merge rules, wiring, test plan, and known decisions. It is intentionally self-contained: a session starting here should not need to re-derive anything.

## 1. Scope

F1 teaches the server to merge Formidable record files (`storage/**/*.meta.json`) key-by-key instead of line-by-line. The merge rule is uniform across all field types: every field's value is atomic, and a disagreement between two sides resolves by last-writer-wins on `meta.updated`. A future live-presence service (SignalR-style, out of scope here) will make same-field races rare; F1's LWW is the last-resort tie-break.

### 1.1 In scope

| Area | F1 deliverable |
| ---- | --------------- |
| Record parsing | JSON → typed `Record` struct with `meta` + `data` maps. |
| `meta.*` merge | §10.2 rules: `updated` (max), `tags` (set-union lowercase+trim), `flagged` (true-wins), `created`/`id`/`template` (immutable → 409), `author_name`/`author_email`/`gigot_enabled` and all other meta keys (follow `updated` winner). |
| `data.*` merge | One rule for every field type: one-side-changed → take changed side; both-same → no-op; both-different → take the side whose `meta.updated` is newer. Atomic value equality is deep-equality on the decoded JSON value. |
| Canonical JSON | Deterministic re-serialisation so two servers with the same inputs produce byte-identical output. |
| Response shape | New 409 body (§10.6), used only for immutable-meta violations. |
| Wiring | Pre-merge hook in `PUT /files/{path}` and `POST /commits` that activates when the marker is present *and* the path matches `storage/**/*.meta.json`. |
| Tests | Unit per meta rule + per merge branch; handler tests for the three observable outcomes; one Cucumber scenario for end-to-end auto-merge. |

### 1.2 Out of scope (explicitly deferred)

- Template parsing — not needed for the uniform rule (F2).
- Schema validation at commit (§10.4) — F2.
- Template writes with `fields[]` structural merge (§10.7) — F2.
- Referential integrity for image fields (§10.5) — F3.
- Live presence / co-editing — separate future service.

## 2. Package layout

New leaf package `internal/formidable/`:

```
internal/formidable/
├── schema.go        # Record type + parser + canonical JSON
├── meta.go          # §10.2 meta.* rules
├── merge.go         # Merge entry point
├── errors.go        # Sentinel errors + FieldConflict / RecordConflict types
├── schema_test.go
├── meta_test.go
└── merge_test.go
```

Import constraints:
- No imports from `internal/server`, `internal/git`, or other project packages. Leaf by construction.
- Stdlib only. No `gopkg.in/yaml.v3` — F1 never loads the template.

## 3. Public types and signatures

### 3.1 schema.go

```go
// Record is the parsed form of storage/<template>/<name>.meta.json.
// Meta and Data are kept as generic maps because the merger never
// needs typed access — it treats each data field's value as atomic.
type Record struct {
    Meta map[string]any `json:"meta"`
    Data map[string]any `json:"data"`
}

// ParseRecord returns a Record from raw JSON bytes. Missing meta or
// data is fine; the caller gets empty maps rather than nil so the
// merger can treat "no data" uniformly.
func ParseRecord(raw []byte) (Record, error)

// CanonicalJSON re-serialises with sorted keys throughout. Required
// for merge output so two servers with the same inputs produce
// byte-identical blobs.
func (r Record) CanonicalJSON() ([]byte, error)
```

### 3.2 meta.go

```go
// MergeMeta applies the §10.2 rules. Returns the merged meta map and
// any conflicts (immutable-field violations are the only conflict
// source). base may be nil on a first-ever write — rules degrade
// gracefully to "take whichever side has it".
func MergeMeta(base, theirs, yours map[string]any) (merged map[string]any, conflicts []FieldConflict)

// UpdatedWinner returns "theirs" or "yours" based on max(meta.updated).
// Exported so merge.go can use the same tie-break for data fields.
// Falls back to "yours" if both timestamps are missing or equal.
func UpdatedWinner(theirs, yours map[string]any) string
```

Rules (pin in `meta_test.go`):

| Key | Rule |
| --- | ---- |
| `updated` | Parse as RFC3339; take max(theirs, yours). Never a conflict. |
| `tags` | Set-union over lowercase-trimmed strings, re-sorted. |
| `flagged` | If divergent, take `true`. |
| `created` | If theirs or yours differs from base, emit `FieldConflict{Scope: "meta", Key: "created", Reason: "immutable"}`. Same for `id` and `template`. |
| `author_name`, `author_email`, `gigot_enabled`, any other key | Follow `UpdatedWinner`. |

### 3.3 merge.go

```go
// Merge is the entry point. It:
//   1. Merges meta via MergeMeta — any immutable violations become
//      the entire conflict list (no data merge happens if meta is
//      corrupt).
//   2. Walks the union of data keys. For each key applies the uniform
//      rule:
//        - neither side changed (or both changed to same value) → keep it
//        - one side changed → take changed side
//        - both changed differently → take UpdatedWinner's value
//   3. Returns the merged record's canonical JSON, or a RecordConflict
//      when meta immutability was violated.
func Merge(path string, base, theirs, yours Record) MergeResult

type MergeResult struct {
    Merged   []byte          // canonical JSON of merged record; set when Conflict is nil
    Conflict *RecordConflict // non-nil only for immutable-meta violations
}
```

Note: no registry, no FieldMerger interface, no deferral. The rule is the same for every key, whether the template knows about it or not.

### 3.4 errors.go

```go
var ErrMalformedRecord = errors.New("formidable: malformed record")

// FieldConflict is one entry in a RecordConflict. In F1 Scope is
// always "meta" — data fields don't produce conflicts. The field is
// kept for forward-compat with future strictness modes.
type FieldConflict struct {
    Scope  string `json:"scope"`             // "meta" only in F1
    Key    string `json:"key"`               // e.g. "created"
    Reason string `json:"reason,omitempty"`  // "immutable"
}

// RecordConflict is the 409 body shape from §10.6. In F1 this only
// appears for immutable-meta violations.
type RecordConflict struct {
    Path           string          `json:"path"`
    CurrentVersion string          `json:"current_version"`
    FieldConflicts []FieldConflict `json:"field_conflicts"`
}
```

## 4. Merge algorithm (pseudocode)

```
Merge(path, base, theirs, yours) →
    mergedMeta, metaConflicts = MergeMeta(base.meta, theirs.meta, yours.meta)
    if len(metaConflicts) > 0:
        return {Conflict: RecordConflict{Path: path, FieldConflicts: metaConflicts}}

    winner = UpdatedWinner(theirs.meta, yours.meta)   # "theirs" or "yours"
    mergedData = {}

    for each key in union(keys(base.data), keys(theirs.data), keys(yours.data)):
        bv, tv, yv = base.data[key], theirs.data[key], yours.data[key]

        if equal(tv, yv):
            mergedData[key] = tv
        else if equal(tv, bv):          # theirs unchanged, yours changed
            mergedData[key] = yv
        else if equal(yv, bv):          # yours unchanged, theirs changed
            mergedData[key] = tv
        else:                            # both changed differently → LWW
            mergedData[key] = tv if winner == "theirs" else yv

    out = Record{Meta: mergedMeta, Data: mergedData}.CanonicalJSON()
    return {Merged: out}
```

`equal` is `reflect.DeepEqual` on the JSON-decoded value. A missing key is `nil`, which compares unequal to a present-but-null value — acceptable for F1 since Formidable writes the whole record each save.

## 5. Wiring into the server

### 5.1 New helper: `internal/server/formidable_merge.go`

```go
// maybeFormidableMerge returns (mergedBlob, nil, true, nil) on a
// successful record merge, (nil, conflict, true, nil) on a
// conflict, or (nil, nil, false, nil) when this write is not a
// candidate (no marker, non-record path, malformed record).
// Error is reserved for transport-level failures.
func (s *Server) maybeFormidableMerge(
    repo, path string,
    parent, head string, // both resolved commit SHAs
    incoming []byte,
) (merged []byte, conflict *formidable.RecordConflict, applicable bool, err error)
```

Decision flow:
1. `isFormidableRepo(repo)` — wraps existing `isValidFormidableMarker` from `formidable_scaffold.go`. Not moved; just called.
2. Path matches `storage/<any>/*.meta.json`. Reject `..` or absolute prefixes.
3. Load base (parent commit) and theirs (HEAD). If parent == head, return `applicable=false` — fast-forward path handles it.
4. `ParseRecord` on all three blobs. Malformed → `applicable=false` (defer to generic path, which will surface the client error).
5. `formidable.Merge(path, base, theirs, yours)`:
   - `Merged` → return as `mergedBlob`; caller rewrites incoming to this and continues through the generic write path, which fast-forwards cleanly.
   - `Conflict` → return as `conflict`; caller emits 409.

### 5.2 Handler changes

**`handleFileWrite` (`handler_sync.go`)** — one branch before the existing `Manager.WriteFile` call:

```go
merged, conflict, applicable, err := s.maybeFormidableMerge(name, path, parent, head.Version, contentBytes)
if err != nil { writeError(w, 500, err.Error()); return }
switch {
case conflict != nil:
    writeJSON(w, 409, conflict)
    return
case applicable:
    opts.Content = merged
    // Fall through; WriteFile sees a fast-forward.
}
```

**`handleRepoCommits`** — same hook applied per-change, only for paths matching `storage/**/*.meta.json`. A conflict on any single change aborts the whole commit with a 409 whose body lists each conflicting path. Reuse Phase 3's `CommitConflictResponse` pattern and let entries carry a `RecordConflict` when applicable.

### 5.3 Config knob

`server.formidable_first` does not gate F1 merging. Per-repo gating comes from the marker file's presence (§2.5). A generic server with a manually-marked repo still benefits. Intentional.

## 6. Response shape (§10.6 alignment)

```json
{
  "current_version": "<sha>",
  "path": "storage/addresses/oak-street.meta.json",
  "field_conflicts": [
    { "scope": "meta", "key": "created", "reason": "immutable" }
  ]
}
```

HTTP status: 409. The body never contains `data.*` conflicts in F1 — data fields always resolve by LWW.

## 7. Dependencies

Stdlib only. No new `go.mod` entries.

## 8. Test plan

### 8.1 Unit (`internal/formidable`)

- **`schema_test.go`** — ParseRecord + CanonicalJSON: happy path, missing meta or data, malformed JSON, canonical-JSON determinism (same input → same bytes, maps sorted).
- **`meta_test.go`** — one table-driven test per rule:
  - `updated` max: equal, one-missing, RFC3339 parse failure fallback.
  - `tags` set-union: case-mixed, duplicates, empty, missing.
  - `flagged` true-wins.
  - `created`/`id`/`template` immutability: base-vs-theirs divergence, base-vs-yours divergence, both divergent (one FieldConflict per key).
  - `author_name`/`author_email`/`gigot_enabled`/arbitrary-extra-key: follow `UpdatedWinner`.
- **`merge_test.go`** — end-to-end on synthetic records:
  - Disjoint data keys on each side → merged with both.
  - Same data key, different values, theirs newer → theirs wins; yours newer → yours wins.
  - Same data key, same value → no-op.
  - Immutable meta violation → Conflict with the key named, no merge attempted.
  - Nested array/object in a data field → treated atomically (deep-equal check, not element-wise).
  - Canonical output bytes stable across runs.

### 8.2 Handler (`internal/server`)

- `formidable_merge_test.go`:
  - `TestPutFileAutoMergesDifferentDataFields` — two clients edit disjoint data fields → 200 with merge commit.
  - `TestPutFileLastWriterWinsOnSameField` — both edit `data.name` → 200, the later `meta.updated` value wins.
  - `TestPutFileImmutableMetaFieldReturns409` — client changes `meta.created` → 409 with `{scope:"meta",key:"created",reason:"immutable"}`.
  - `TestPutFileSkipsWhenNoMarker` — same path on a non-Formidable repo uses generic merge, no `RecordConflict` shape.
  - `TestCommitsAggregatesRecordConflicts` — multi-file commit with one corrupt record → 409 listing only the offending path.

### 8.3 Cucumber (`integration/features/formidable_merge.feature`)

```gherkin
Scenario: Two clients editing disjoint data fields on the same record auto-merge
  Given the server is running in formidable-first mode
  And I POST "/api/repos" with body '{"name":"m1"}'
  And I seed record "addresses/oak.meta.json" with data {"name":"Oak","country":"nl"}
  And I GET "/api/repos/m1/head"
  And I save the JSON response "version" as "head0"
  And I PUT .../files/storage/addresses/oak.meta.json with data {"name":"Oak Rd"} and parent ${head0}
  When I PUT .../files/storage/addresses/oak.meta.json with data {"country":"uk"} and parent ${head0}
  Then the response status should be 200
  And the resulting record at HEAD has data.name "Oak Rd" and data.country "uk"
```

New step definitions:
- `I seed record "<path>" with data <json>` — writes via the existing PUT handler so seeding exercises the same stack.
- `the resulting record at HEAD has data.<key> "<value>"` (repeatable; asserts final merged bytes).

### 8.4 Swagger

Document the new 409 body shape on `PUT /files/{path}` and `POST /commits`:

```go
// @Failure 409 {object} formidable.RecordConflict "Record merge conflict (immutable meta field)"
```

Coexists with the existing `WriteFileConflictResponse` — they appear as two `@Failure 409` entries distinguished by schema. Regenerate with `swag init -g main.go -o docs`.

## 9. Known decisions (already made)

1. **Leaf package, no project imports.** `internal/formidable` is reusable and unit-testable in isolation.
2. **Template parsing deferred.** F1 never loads the template — the uniform rule doesn't need to know field types. F2 adds parsing when validation enters the picture.
3. **One rule for all data fields.** No per-type dispatch, no Registry, no FieldMerger interface. Motivated by the upcoming live-presence service, which makes same-field races rare enough that LWW is acceptable.
4. **Conflicts are meta-only.** In F1 the 409 shape is only used for immutable `meta.created`/`meta.id`/`meta.template` violations.
5. **`gigot_enabled` follows `updated` winner.** F2 may replace with cross-file re-stamp from the template. Noted in the meta.go docstring.
6. **Marker presence, not `formidable_first` config, gates the merge.** Per-repo opt-in via marker is already the §2.5 contract.
7. **Canonical JSON output.** Keys sorted throughout; array positions preserved. Guarantees byte-stable merge output across servers — mirror-sync will depend on this.

## 10. Open questions

None blocking. Notes for future phases:
- Extraneous `data.*` keys are accepted today. F2 may add a strictness knob.
- Clock skew on `meta.updated` is accepted as-is — LWW uses wall-clock. The live-presence service will make this a non-issue in practice.

## 11. Acceptance checklist

F1 is done when:

- [ ] `internal/formidable/` exists with the four files from §2, each covered by unit tests.
- [ ] `internal/server/formidable_merge.go` wires the helper and both handlers (`PUT /files`, `POST /commits`) call it before the generic path.
- [ ] One Cucumber scenario demonstrates end-to-end auto-merge of disjoint data fields.
- [ ] Handler tests cover: auto-merge, LWW on same field, immutable meta 409, marker-absent skip, commit aggregate.
- [ ] Swagger regenerates cleanly with the new 409 schema documented.
- [ ] README roadmap: F1 shipped note, F2 scope summary added.
- [ ] Design doc §11 F1 status line: "Phase F1 shipped YYYY-MM-DD".
- [ ] `go test ./... -count=1` green.

## 12. Reference material

Authoritative:
- `docs/design/structured-sync-api.md` §10 (record model), §10.2 (meta rules), §10.3 (uniform field rule), §10.6 (conflict shape), §11 (F1 scope).
- `internal/server/formidable_scaffold.go` — `isValidFormidableMarker` (reuse, do not move).
- `internal/git/write.go` — generic `WriteFile` (do not modify).

Okay to consult for contract detail (if a question arises that §10 doesn't answer):
- `~/Projects/Formidable/schemas/meta.schema.js` — record shape on the client.
- `~/Projects/Formidable/controls/fileManager.js`, `controls/configManager.js` — storage path conventions (`storage/<template-filename-sans-.yaml>/`).

Do **not** consult:
- `~/Projects/Formidable/storage/`, `~/Projects/Formidable/examples/templates/*.yaml` — incidental complexity the server does not model. Use synthetic fixtures.
