# F1a — Formidable-first scalar field merge (prep)

**Status:** prep, not yet implemented. Written 2026-04-18 so the next session can pick up cold.

This document pins F1a's scope, package layout, public API, merge rules, wiring points, test plan, and known decisions. It is intentionally self-contained: a session starting here should not need to re-derive anything. Open design questions are called out explicitly.

## 1. Why split F1 into F1a + F1b

F1 in `structured-sync-api.md` §11 covers "schema parsing + per-field record merge" — all 17 field types plus collection merging plus line-based 3-way for prose. That is too wide for one slice. F1a ships the scalar-field subset and the surrounding machinery; F1b fills in textarea/code/latex/tags/list/table/loop/api/image.

F1a alone satisfies the two headline acceptance criteria from §11 for the common case:

> - Two clients edit different fields on the same record with stale parents ⇒ auto-merge succeeds, one merge commit, no human intervention.
> - Two clients edit the same scalar field ⇒ 409 naming the field only.

It does **not** yet satisfy the textarea / collection / rejection-on-corrupt-record cases. F1b covers those.

## 2. Scope boundaries

### 2.1 In scope (F1a)

| Area | F1a deliverable |
| ---- | --------------- |
| Template parsing | YAML → typed `Template` struct. Only the `fields[]` ordered list and its `key`, `type`, `primary_key` attrs are structurally load-bearing for merging; others are captured but not read. |
| Record parsing | JSON → typed `Record` struct with `meta` + `data` maps. |
| `meta.*` merge | All rules from §10.2: updated (max), tags (set-union), flagged (true-wins), created/id/template (immutable → 409), author_name/email (follow updated winner), gigot_enabled (follow updated winner — F1a shortcut; F2 cross-file-re-stamp deferred). |
| Scalar `data.*` merge | Nine field types sharing one rule: text, number, boolean, date, range, dropdown, radio, guid, link. Rule per §10.3: one-side-unchanged → take changed side; both-same → no-op; both-different → 409. |
| Dispatch | Registry keyed by field type name; one `ScalarMerger` registered for all nine. |
| Response shape | New `RecordConflictResponse` per §10.6. Old `WriteFileConflictResponse` stays untouched. |
| Wiring | Pre-merge hook in `PUT /files/{path}` and `POST /commits` that activates only when marker present + path matches `storage/**/*.meta.json` + governing template loads. |
| Fallback | If any `data.*` field touched on both sides is not in the scalar set, defer the whole record to the existing generic merge. Conservative — no regression. |
| Tests | Unit per merger + per meta rule; handler test for two-client happy path; handler test for same-field 409; one Cucumber scenario for auto-merge success. |

### 2.2 Out of scope (F1a) — explicitly deferred

- Line-based 3-way merge for `textarea`, `markdown`, `code`, `latex`. Deferred to F1b.
- Collection merging: `tags` data field (note: `meta.tags` IS in F1a), `multioption`, `list`, `table`, `loopstart`/`loopstop`. Deferred to F1b.
- `api` deep-merge, `image` last-writer-wins coordinated with §10.5. Deferred to F1b / F3.
- Schema validation at commit (§10.4) — F2 territory.
- Template writes with `fields[]` structural merge (§10.7) — F2.
- Referential integrity for image fields (§10.5) — F3.
- `gigot_enabled` cross-file re-stamp from template — F2 (F1a takes the updated-winner value as an approximation).

## 3. Package layout

New leaf package `internal/formidable/`:

```
internal/formidable/
├── schema.go        # Template, Record, FieldDef types + parsers
├── meta.go          # §10.2 meta.* rules
├── fields.go        # FieldMerger interface + registry + dispatch
├── scalar.go        # ScalarMerger implementing the 9-type rule
├── merge.go         # Merger struct, the entry point: Merge(base, theirs, yours, tmpl) → result
├── errors.go        # Sentinel errors + FieldConflict / RecordConflict types
├── schema_test.go
├── meta_test.go
├── fields_test.go
├── scalar_test.go
└── merge_test.go
```

Import constraints:
- No imports from `internal/server`, `internal/git`, or any other project package. Leaf by construction.
- Depends on: stdlib + `gopkg.in/yaml.v3` (add to `go.mod` — `v2` is already indirect, `v3` is in `go.sum` as indirect too).

## 4. Public types and signatures

All types below live in `internal/formidable`. These are the signatures the next session should implement to. Keep the package surface narrow — do not export helpers unless a test package needs them.

### 4.1 schema.go

```go
// Template is the parsed form of templates/<name>.yaml. F1a only reads
// Fields; other fields are captured for forward-compatibility so F1b/F2
// don't have to touch the parser.
type Template struct {
    Name             string      `yaml:"name"`
    Filename         string      `yaml:"filename"`
    EnableCollection bool        `yaml:"enable_collection"`
    ItemField        string      `yaml:"item_field"`
    MarkdownTemplate string      `yaml:"markdown_template"`
    SidebarExpr      string      `yaml:"sidebar_expression"`
    GigotEnabled     bool        `yaml:"gigot_enabled"`
    Fields           []FieldDef  `yaml:"fields"`
}

// FieldDef is the subset of a field definition F1a needs. Extra YAML
// keys (options, default, description, two_column, readonly, etc.)
// are intentionally ignored — captured by a trailing `Extras
// map[string]any ,inline` if forward-compat becomes load-bearing in F1b.
type FieldDef struct {
    Key        string `yaml:"key"`
    Type       string `yaml:"type"`
    PrimaryKey bool   `yaml:"primary_key"`
}

// Record is the parsed form of storage/<template>/<name>.meta.json.
// Meta and Data are kept as generic maps because only typed merge rules
// (in meta.go / scalar.go) inspect specific keys — avoids a strict
// struct that would reject forward-compat fields at parse time.
type Record struct {
    Meta map[string]any `json:"meta"`
    Data map[string]any `json:"data"`
}

// ParseTemplate returns a Template from raw YAML bytes. Returns
// ErrMalformedTemplate wrapping the underlying YAML error.
func ParseTemplate(raw []byte) (Template, error)

// ParseRecord returns a Record from raw JSON bytes. Returns
// ErrMalformedRecord wrapping the underlying JSON error.
// A record with neither meta nor data is still returned (empty maps)
// so the merger can treat it as "no data" rather than failing upfront.
func ParseRecord(raw []byte) (Record, error)

// CanonicalJSON re-serialises a Record with sorted keys for maps the
// schema does not order. Mandatory for emitting merge output so two
// servers merging the same inputs produce byte-identical blobs.
func (r Record) CanonicalJSON() ([]byte, error)
```

### 4.2 meta.go

```go
// MergeMeta applies §10.2 rules. Returns the merged meta map and any
// conflicts (immutable-field violations are the only conflict source
// in F1a's meta rules). Caller-side: base may be nil if this is a
// first-ever write, in which case updated/tags/flagged rules degrade
// gracefully to "take whichever side has it".
func MergeMeta(base, theirs, yours map[string]any) (merged map[string]any, conflicts []FieldConflict)
```

Rules (pin these in meta_test.go):

| Key              | Rule |
| ---------------- | ---- |
| `updated`        | parse as RFC3339; take max(theirs, yours). Never a conflict. |
| `tags`           | set-union over normalised (lowercase + trim) strings, re-sorted ascending. |
| `flagged`        | if divergent, take `true`. |
| `created`        | must not change after creation; if theirs or yours differs from base, emit `FieldConflict{Scope: "meta", Key: "created", Reason: "immutable"}`. |
| `id`             | same as `created`. |
| `template`       | same as `created`. |
| `author_name`    | follow `updated` winner (last-writer-wins on attribution). |
| `author_email`   | same as `author_name`. |
| `gigot_enabled`  | F1a: follow `updated` winner. F2 will replace with cross-file re-stamp. |
| any other meta.* | set-union of keys; on value disagreement, follow `updated` winner (not a conflict in F1a). |

### 4.3 fields.go

```go
// FieldMerger resolves a single data.<key> across base/theirs/yours.
// Implementations look only at the three values plus the FieldDef;
// they never parse YAML or JSON. Keeps the merge logic purely
// data-oriented and unit-testable without fixtures.
type FieldMerger interface {
    Merge(key string, base, theirs, yours any, def FieldDef) MergeOutcome
}

type MergeOutcome struct {
    // Resolved carries the merged value when Applicable == true and
    // Conflict == nil. Deliberately `any` — scalar values stay as
    // whatever JSON decoded them to (float64 for numbers, bool, string).
    Resolved  any
    Conflict  *FieldConflict
    // Applicable is false when the merger doesn't handle this field
    // type. The dispatcher treats Applicable == false as a signal to
    // defer the whole record to generic merge (conservative F1a rule).
    Applicable bool
}

// Registry returns the default field-merger registry. F1a registers
// ScalarMerger under the nine scalar type names. F1b adds more entries
// without modifying this function — prefer NewRegistry().With(...)
// patterns over mutating the default.
func DefaultRegistry() Registry

type Registry struct { /* unexported fields */ }

func (r Registry) Lookup(fieldType string) (FieldMerger, bool)
func (r Registry) With(fieldType string, m FieldMerger) Registry
```

### 4.4 scalar.go

```go
// ScalarMerger implements the single rule shared by nine field types
// (§10.3 top row): text, number, boolean, date, range, dropdown,
// radio, guid, link. Rule:
//   - theirs == base, yours != base → Resolved = yours
//   - yours == base, theirs != base → Resolved = theirs
//   - theirs == yours               → Resolved = theirs (no-op)
//   - theirs != yours               → Conflict with blob triple
// Equality is deep-equality on the decoded JSON value.
type ScalarMerger struct{}

// Enforces the contract above.
func (ScalarMerger) Merge(key string, base, theirs, yours any, def FieldDef) MergeOutcome
```

### 4.5 merge.go

```go
// Merger is the entry point wired from the server. One instance per
// server process is fine — it holds only the field-merger Registry.
type Merger struct {
    Fields Registry
}

func NewMerger() *Merger { return &Merger{Fields: DefaultRegistry()} }

// Merge is the top-level per-record merge. It:
//   1. Applies §10.2 meta rules.
//   2. Walks data.* keys, dispatches to the field merger.
//   3. If any field's merger returns Applicable=false AND both sides
//      changed that field, returns Result{Deferred: true} so the
//      caller falls back to generic line-based merge. If only one
//      side changed, the registry-less key still resolves (take the
//      changed side).
//   4. Otherwise returns the merged Record bytes (via CanonicalJSON)
//      or a RecordConflict collecting every field-level conflict.
func (m *Merger) Merge(tmpl Template, base, theirs, yours Record) MergeResult

type MergeResult struct {
    // Exactly one of Merged / Conflict / Deferred is set.
    Merged   []byte          // canonical JSON of merged record
    Conflict *RecordConflict // at least one field conflict; F1a bundles all
    Deferred bool            // at least one non-scalar field was touched both sides
}
```

### 4.6 errors.go

```go
var (
    ErrMalformedTemplate = errors.New("formidable: malformed template")
    ErrMalformedRecord   = errors.New("formidable: malformed record")
    ErrUnknownTemplate   = errors.New("formidable: record.meta.template does not resolve")
)

// FieldConflict is one entry in a RecordConflict. Scope is "meta" or
// "data" so a client can distinguish a metadata immutability violation
// from a data-field disagreement.
type FieldConflict struct {
    Scope  string `json:"scope"`           // "meta" or "data"
    Key    string `json:"field"`           // e.g. "name", "created"
    Reason string `json:"reason,omitempty"` // "immutable" for meta; omitted for data
    BaseB64   string `json:"base_b64,omitempty"`   // only for data conflicts
    TheirsB64 string `json:"theirs_b64,omitempty"`
    YoursB64  string `json:"yours_b64,omitempty"`
}

// RecordConflict is the aggregate response body for a failed record
// merge. Maps 1:1 to the §10.6 per-field conflict shape.
type RecordConflict struct {
    Path      string          `json:"path"`
    Conflicts []FieldConflict `json:"conflicts"`
}
```

## 5. Merge algorithm (pseudocode)

```
Merge(tmpl, base, theirs, yours) →
    mergedMeta, metaConflicts = MergeMeta(base.meta, theirs.meta, yours.meta)

    mergedData = {}
    dataConflicts = []
    deferred = false

    for each key in union(keys(base.data), keys(theirs.data), keys(yours.data)):
        bv, tv, yv = base.data[key], theirs.data[key], yours.data[key]
        fieldDef = tmpl.Fields.lookup(key)   # nil if key not in template

        if fieldDef == nil:
            # Extraneous data.* key. §11 F1a leaves strictness knob for
            # F1b; F1a takes last-writer-wins on the updated winner.
            mergedData[key] = tie-break using mergedMeta.updated winner
            continue

        merger, ok = m.Fields.Lookup(fieldDef.Type)
        if !ok or !merger.Applicable:
            # Only defer if both sides actually changed this field.
            if changed(bv, tv) && changed(bv, yv) && !equal(tv, yv):
                deferred = true
                break
            # One side unchanged → take the changed side (trivial rule,
            # works for any type).
            mergedData[key] = pickChangedSide(bv, tv, yv)
            continue

        outcome = merger.Merge(key, bv, tv, yv, fieldDef)
        if outcome.Conflict != nil:
            dataConflicts.append(outcome.Conflict)
        else:
            mergedData[key] = outcome.Resolved

    if deferred: return {Deferred: true}
    if len(metaConflicts) + len(dataConflicts) > 0:
        return {Conflict: RecordConflict{Path: ..., Conflicts: metaConflicts+dataConflicts}}
    
    out = Record{Meta: mergedMeta, Data: mergedData}.CanonicalJSON()
    return {Merged: out}
```

Note: the "pick the changed side" helper is a data-agnostic equality check on the decoded JSON value — it works for any field type without understanding its semantics. That's why the unregistered-type path can still resolve many real cases without deferring.

## 6. Wiring into the server

### 6.1 Where to intercept

F1a does **not** change `internal/git/WriteFile` or the generic merge in `internal/git`. It adds one helper in `internal/server/` that runs before the generic write path and, when applicable, pre-computes a merged blob so `WriteFile` only sees a fast-forward.

**New file:** `internal/server/formidable_merge.go`

```go
// maybeFormidableMerge returns (mergedBlob, nil, true, nil) on a
// successful formidable field merge, (nil, conflict, true, nil) when
// field-level conflicts were found, or (nil, nil, false, nil) when
// this write is not a candidate (no marker, non-record path,
// unresolvable template, or a deferred record). Error return is
// reserved for malformed template/record — propagate as 422.
func (s *Server) maybeFormidableMerge(
    repo, path string,
    parent, head string, // both resolved commit SHAs
    incoming []byte,
) (merged []byte, conflict *formidable.RecordConflict, applicable bool, err error)
```

Decision flow:

1. `isFormidableRepo(repo)` — checks marker at HEAD (reuses `isValidFormidableMarker` from `internal/server/formidable_scaffold.go`; wrap the existing code, do not move it).
2. Path matches `storage/<any>/*.meta.json`. Reject paths with `..` or absolute prefixes here, even though the generic write path already does.
3. Load three blobs via `Manager.File`:
   - `base` at the parent commit
   - `theirs` at HEAD
   - `yours` is the incoming bytes
4. Parse `record := ParseRecord(yours)`, pull `record.Meta["template"]`. If missing, return `applicable=false` (can't do a Formidable merge without knowing the template — defer).
5. Load `templates/<template-filename>` at HEAD. If absent, return `applicable=false` (same reason — defer).
6. `formidable.NewMerger().Merge(tmpl, base, theirs, yours)`:
   - `Merged` → return as mergedBlob, caller does a plain fast-forward write of that blob.
   - `Conflict` → return as conflict, caller emits 409 with RecordConflict JSON.
   - `Deferred` → return `applicable=false`, caller falls back to generic 3-way.

### 6.2 PUT /files/{path} changes

`handler_sync.go :: handleFileWrite` — one additional branch before the existing `Manager.WriteFile` call:

```go
merged, conflict, applicable, err := s.maybeFormidableMerge(name, path, parent, head.Version, contentBytes)
if err != nil {
    writeError(w, 422, err.Error())
    return
}
switch {
case conflict != nil:
    writeJSON(w, 409, conflict)
    return
case applicable:
    // Formidable gave us a pre-merged blob; rewrite opts.Content so
    // WriteFile's own merge path finds no conflict.
    opts.Content = merged
    // Fall through to Manager.WriteFile — it will do the fast-forward
    // or a clean 3-way against HEAD.
}
// (Generic path continues here unchanged.)
```

### 6.3 POST /commits changes

`handler_sync.go :: handleRepoCommits` applies the same hook per-change, only for changes whose path matches `storage/**/*.meta.json`. If any single change comes back as a conflict, the whole commit aborts 409 with an aggregate body — reuse the existing transactional-409 pattern from Phase 3 (`CommitConflictResponse`) but carry a `RecordConflict` in each entry alongside or instead of `WriteFileConflict`. Keep the two conflict shapes distinguishable by an `op`/`kind` tag.

### 6.4 Config knob

`server.formidable_first` does not gate F1a merging. Per-repo gating comes from the marker file's presence (§2.5). That means a generic server with a manually-marked repo still benefits from field merging. Intentional.

## 7. Response shapes (§10.6 alignment)

Extending OpenAPI with one new response schema. Add Swagger annotations alongside the handler changes and regenerate (`swag init -g main.go -o docs`).

```json
{
  "path": "storage/addresses/oak-street.meta.json",
  "conflicts": [
    {
      "scope": "data",
      "field": "country",
      "base_b64": "InVzIg==",
      "theirs_b64": "InVrIg==",
      "yours_b64": "ImNhIg=="
    },
    {
      "scope": "meta",
      "field": "created",
      "reason": "immutable"
    }
  ]
}
```

HTTP status: 409.

## 8. Dependencies

- `gopkg.in/yaml.v3` — add as a direct dep in `go.mod` (currently indirect in `go.sum`). No alternatives considered — v3 is the canonical parser for structured YAML in Go.

No other new dependencies. JSON is stdlib.

## 9. Test plan

### 9.1 Unit tests (package `formidable`)

- `schema_test.go` — ParseTemplate / ParseRecord / CanonicalJSON: minimum three fixtures per function (happy path, missing-required-field, malformed input).
- `meta_test.go` — one test per §10.2 rule. Table-driven. Pin behaviour of:
  - `updated` with equal timestamps (no-op), one-missing (take the present), clock-skew scenarios.
  - `tags` with empty/missing/case-mixed/dupe inputs.
  - `flagged` true-wins.
  - `created`/`id`/`template` immutability: exactly one FieldConflict emitted per divergent key.
  - `author_name`/`author_email`/`gigot_enabled` follow `updated` winner.
- `fields_test.go` — Registry Lookup/With + dispatcher behaviour on unregistered types.
- `scalar_test.go` — the 4-way rule table, one row per rule outcome:
  - both unchanged → no-op
  - theirs changed only → take theirs
  - yours changed only → take yours
  - both changed same → take theirs (no-op)
  - both changed different → conflict with blob triple
  Run the table for at least `text`, `number`, `boolean`, `date` to catch JSON-decode-type quirks (float64 vs int, bool vs string "true").
- `merge_test.go` — end-to-end on synthetic records:
  - Different fields both sides, scalar types only → Merged with both changes.
  - Same scalar field divergent → Conflict with one entry.
  - Mixed: scalar change + textarea change (unregistered in F1a) → Deferred.
  - Extraneous `data.*` key (not in template) → takes updated-winner side.
  - Record with no `meta.template` → returns error or `Applicable=false` — pick one, pin behaviour.

### 9.2 Handler tests (package `internal/server`)

- `formidable_merge_test.go`:
  - `TestPutFileAutoMergesDifferentScalarFields` — seed record with two scalars, two clients edit disjoint scalars, second PUT returns 200 with `merged_from`/`merged_with` (reusing existing Phase 2 response shape since this is still a merge, not a conflict).
  - `TestPutFileConflictsOnSameScalarField` — both clients edit `data.name` → 409 body shape matches `RecordConflict`.
  - `TestPutFileImmutableMetaFieldReturns409` — client attempts to change `meta.created` → 409 with `{scope:"meta", field:"created", reason:"immutable"}`.
  - `TestPutFileFallsBackToGenericForTextarea` — textarea edit on both sides in F1a goes through generic line merge (behaviour preserved; add note that F1b replaces this test).
  - `TestPutFileSkipsWhenNoMarker` — same record path on a non-Formidable repo uses the generic merge, no RecordConflict shape.

### 9.3 Cucumber (`integration/features/formidable_merge.feature`)

One scenario at the wire level:

```gherkin
Scenario: Two clients editing disjoint scalar fields on the same record auto-merge
  Given the server is running in formidable-first mode
  And I POST "/api/repos" with body '{"name":"m1"}'
  And I seed record "addresses/oak.meta.json" under template "addresses.yaml"
  And I GET "/api/repos/m1/head"
  And I save the JSON response "version" as "head0"
  And I PUT .../files/storage/addresses/oak.meta.json (client A edits data.name) with parent ${head0}
  When I PUT .../files/storage/addresses/oak.meta.json (client B edits data.country) with parent ${head0}
  Then the response status should be 200
  And the response body should contain "merged_from"
```

New step definitions needed:
- `I seed template "<path>" with YAML ...` (multi-line body support)
- `I seed record "<path>" under template "<name>"`
Both write via the existing PUT handler so seeding exercises the same stack.

### 9.4 Swagger

- Document the new 409 body shape on `PUT /files/{path}` and `POST /commits` via `@Failure 409 {object} formidable.RecordConflict` annotations, then regenerate.
- Coexist with the existing `WriteFileConflictResponse`: keep them as two separate `@Failure 409` entries distinguishable by their schema.

## 10. Known decisions (already made)

1. **Leaf package, no project imports.** Makes `formidable` reusable and testable in isolation.
2. **`gopkg.in/yaml.v3`**, not v2. v3 preserves key order in unmarshal which F1b will need for fields[].
3. **One `ScalarMerger` for nine types**, not nine identical mergers. DRY.
4. **Conservative defer for unknown/mixed-type records.** No partial merges in F1a — simpler mental model, no risk of dropping non-scalar changes.
5. **Marker presence, not `formidable_first` config, gates the merge.** Per-repo opt-in via marker is already the §2.5 contract.
6. **Canonical JSON output.** Guarantees identical bytes across server restarts and across two servers that see the same inputs — future mirror-sync work depends on this.
7. **Response shape: new DTO, not an extension of WriteFileConflictResponse.** The two conflict kinds address different callers (one file blob vs per-field structured).
8. **`gigot_enabled` F1a shortcut = follow updated winner.** F2 replaces with cross-file re-stamp. Note in the docstring.

## 11. Open questions (raise at session start)

1. **Extraneous `data.*` keys — warn or reject?** §11 F1 says "configurable strictness". F1a punts (lenient: take updated-winner). Do we need the knob in F1a, or defer to F2?
2. **`gigot_enabled` in F1a** — is updated-winner acceptable, or should we already implement cross-file re-stamp from the template? The latter doubles F1a's wiring complexity. Recommendation: accept updated-winner for F1a, add a `TODO(F2)` in `meta.go`.
3. **Templates under a nested directory?** §10.1 implies `templates/*.yaml` flat. Confirm F1a assumes flat layout; deeper nesting would change the template-lookup code path.
4. **Record path layout.** §10.1 says `storage/<template>/*.meta.json`. The `<template>` segment — is it the template filename sans `.yaml`, or the template's `name` field, or the filename with `.yaml`? F1a implementation assumes the filename sans `.yaml` (e.g. `storage/addresses/` for `templates/addresses.yaml`). Pin this before writing tests.
5. **Clock skew on `updated`.** If two clients are seconds apart, the later wall-clock wins — even if semantically the other was "more recent" in the user's intent. Acceptable for F1a? (Note: we don't have a better signal.)

## 12. Acceptance checklist

F1a is done when:

- [ ] `internal/formidable/` exists with the six files from §3, each covered by unit tests.
- [ ] `internal/server/formidable_merge.go` wires the helper and both handlers (`PUT /files`, `POST /commits`) call it before the generic path.
- [ ] One feature-layer Cucumber scenario demonstrates end-to-end auto-merge of disjoint scalar fields.
- [ ] Two handler tests demonstrate 409 + field-name on same-scalar-field collision and on immutable meta change.
- [ ] Swagger regenerates cleanly with the new 409 schema documented.
- [ ] README roadmap moves "Structured sync API (multi-phase)" forward with an F1a-shipped note, and adds F1b to the backlog with scope.
- [ ] Design doc §11 status line updated: "Phase F1a shipped YYYY-MM-DD".
- [ ] Full test suite green with `go test ./... -count=1`.

## 13. Reference material

Authoritative:
- `docs/design/structured-sync-api.md` §10 (record/template model), §11 (F1-F4 scope).
- `internal/server/scaffold/formidable/templates/basic.yaml` — existing minimal template, use as a F1a test fixture.
- `internal/server/formidable_scaffold.go` — `isValidFormidableMarker` (reuse, do not move).
- `internal/git/write.go` — generic `WriteFile` (do not modify).

Okay to consult for contract detail:
- `~/Projects/Formidable/schemas/` — meta/template/field schemas.
- `~/Projects/Formidable/controls/` — especially `formManager.js`, `fileManager.js`, `gitManager.js`.
- `~/Projects/Formidable/modules/handlers/`.

Do **not** consult:
- `~/Projects/Formidable/storage/`, `~/Projects/Formidable/examples/templates/*.yaml` — incidental complexity the server does not model. Use synthetic fixtures written from §10.
