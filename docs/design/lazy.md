# GG.lazy

Status: **slice 1 + 2 + opportunistic migrations shipped 2026-05-02.**
Generic `data-*`-attribute-driven render helper for the admin UI.
Replaces per-page imperative DOM glue (`querySelector` +
`addEventListener` setups that build the same shape in three places)
with declarative bindings: HTML carries the intent, one tiny JS
module reads it.

In the same family as `GG.tag_picker`, `GG.tag_filter`,
`GG.text_filter`, `GG.drawer`, and `GG.toggle_switch` — page code
stays thin, the helper owns the fetch / template / event wiring.

Real-world fragments shipped to date (in
`internal/server/templates/fragments/`):

- `abilities.html` — abilities collapse body on subscription cards
- `account-detail.html` — accounts table detail row
- `create-account.html` / `create-credential.html` /
  `create-repository.html` / `create-tag.html` / `edit-credential.html` /
  `issue-subscription.html` / `rename-tag.html` — drawer form bodies
- `repo-card-body.html` — repository card details body
- `repo-subscriptions.html` — repo card's nested subs collapse
- `token-card-body.html` — admin subscription card body
- `user-subscription-card.html` — `/user` page card body

---

## 1. Why

Three nudges across the tags slice 3 session converged on the same
preference: HTML carries intent via `data-*`, generic JS reads it.

- `data-role="admin"` on `.badge` replaced class proliferation
  (`.badge.formidable / .maintainer / .regular`).
- `GG.tag_picker.mount(host, opts)` and
  `GG.tag_filter.attachClientSide({...})` replaced the per-page
  copies of "build the chip rows + bind clicks + manage URL state".

Where this still hurts:

- **Lazy detail panes.** Today every "show me X for this row" pattern
  is hand-rolled — fetch, build markup, attach handlers — and lands
  five lines deep in the page-specific JS.
- **Submit + render-back.** The PATCH /api/admin/tokens echo we
  shipped in slice-3 polish proved the pattern of "submit returns
  the new state, client patches in place." Right now the only way
  to use it is to write the imperative wiring per call site.

`GG.lazy` is the helper that lifts both into one shape.

---

## 2. Hello-world example

```html
<details data-lazy-tpl="abilities"
         data-lazy-submit="/api/admin/tokens"
         data-lazy-submit-method="PATCH"
         data-lazy-after="render"
         data-token="abc123">
  <summary>Abilities</summary>
</details>
```

The data for the abilities picker is supplied programmatically via a
`getData(host)` callback the page registers with `GG.lazy.bind()` —
not via `data-lazy-src`. Subscription tokens are bearer credentials,
so we deliberately don't put them in URL paths (they'd leak to
access logs and browser history; see `feedback_subscription_key_per_user.md`).
The data already sits in memory on the page from the token list
fetch — the helper just reads it from there.

For surfaces where the entity ID is safe to log (account
`provider:identifier`, repo names), `data-lazy-src` is the right
choice and the helper fetches it directly.

```html
<!-- internal/server/templates/fragments/abilities.html -->
<div class="ability-picker">
  {{#each abilities}}
    <label class="switch-row">
      <input type="checkbox" name="ability" value="{{name}}" {{checked}}>
      <span class="control-label">{{name}}</span>
      <span class="muted ability-hint">{{hint}}</span>
    </label>
  {{/each}}
</div>
<div class="abilities-foot">
  <button type="button" class="small ability-save" data-lazy-action="submit">Save</button>
</div>
```

```js
// In admin_common.js boot, exactly once.
GG.lazy.attachAll();
```

Behaviour:

1. User opens the `<details>`. `GG.lazy` fetches
   `/api/admin/tokens/abc123/abilities` (the URL has the host's
   `data-token` substituted).
2. Helper fetches the `abilities` fragment (cached after first use).
3. Substitutes `{{...}}` against the JSON response, clones into the
   `<details>` body.
4. User toggles checkboxes, clicks Save → helper collects every
   `[name]` input value, packages with the host's `data-*`
   attributes as `{token: "abc123", abilities: [...]}`, PATCHes to
   `/api/admin/tokens`.
5. With `data-lazy-after="render"`: re-runs the template against
   the response body so the rendered chips reflect the canonical
   post-update state. (Echo pattern, see PATCH /api/admin/tokens
   slice 3 contract.)

---

## 3. Server side

### 3.1 Fragments folder

Templates live in `internal/server/templates/fragments/*.html`,
embedded alongside the existing full-page templates:

```
internal/server/templates/
  admin.html              # full pages, server-rendered with html/template
  accounts.html           #   ({{.Version}}, etc.)
  ...
  fragments/              # client-rendered partials, served raw
    abilities.html
    account-detail.html
    revoke-by-tag.html
```

```go
//go:embed templates/fragments/*.html
var fragmentsFS embed.FS
```

Two clean conventions:

- **Server-rendered (existing `templates/*.html`):** Go `html/template`
  syntax — `{{.Field}}`, with the page model as context.
- **Client-rendered (`templates/fragments/*.html`):** GG.lazy
  `{{key}}` syntax, served raw.

Both use double braces; they don't overlap because each file is
processed by exactly one engine. A page template is never fetched
through `/fragments/`; a fragment is never rendered through Go's
`html/template`.

### 3.2 Route

`GET /fragments/{name}` serves a fragment by name.

- **Auth:** admin-session gated. Fragments don't carry user data,
  but they encode admin-UI shape (which inputs exist, what gets
  PATCHed where), and a leak gives an attacker a head start on
  recon. Same posture as the rest of `/admin/*`.
- **Caching:** strong ETag derived from a build-time hash of the
  fragment file. `Cache-Control: no-cache, must-revalidate` so the
  browser sends `If-None-Match` on every load and gets a `304 Not
  Modified` after the first fetch. Net cost per fragment per
  release: one tiny round trip.
- **Content-Type:** `text/html; charset=utf-8`.
- **404:** unknown names return `{"error": "fragment not found"}`.

### 3.3 Why no Go-template processing on fragments

Fragments are rendered against API response data, not server-side
page state. Adding Go's `html/template` step on the server would
mean two layers of substitution running on the same `{{...}}`
syntax — confusing, easy to leak server state into a client-side
placeholder. Raw fragments + client-side substitution is one source
of truth.

If a fragment ever needs server-side data (the build version, e.g.),
we add a separate `data-lazy-meta` attribute on the host (`data-version="0.9.12"`)
or expose it via a tiny `/api/admin/meta` endpoint. We don't grow
the fragment pipeline.

---

## 4. Client side

### 4.1 Public API

```js
GG.lazy.bind(host, opts);
// Bind one host element. opts: {
//   getData: (host) => object | Promise<object>,   // optional — synchronous data hook
//                                                  // (mutually exclusive with data-lazy-src)
//   onRendered: (host, data) => void,              // optional — called after each render
// }

GG.lazy.attachAll(root?);
// Walk the document (or the given root) for `[data-lazy-tpl]` and
// auto-bind any element that has no programmatic getData. Elements
// whose getData is supplied via bind() are skipped here.

GG.lazy.refresh(host);
// Force re-fetch + re-render, ignoring cache.

GG.lazy.cache.clear();
// Drop in-memory fragment cache (for tests + dev hot-reload).
```

Two ways to feed data into a host:

- **`data-lazy-src` attribute** — declarative, fetched on trigger.
  URL placeholders (`{key}`) substitute from the host's `data-*`
  attributes. Attach via `GG.lazy.attachAll()`.
- **`getData(host)` callback** — programmatic, called on trigger.
  Returns the JSON-shaped object the template will render against.
  Attach via `GG.lazy.bind(host, { getData })`. Used when the data
  is already in memory or the URL contains values that can't be
  logged (bearer tokens).

A host MUST use exactly one path. Specifying both is a config
error and the helper throws at bind time.

### 4.2 Attribute reference

| Attribute                       | On    | Purpose                                                          |
| ------------------------------- | ----- | ---------------------------------------------------------------- |
| `data-lazy-src="/path/{key}"`   | host  | GET endpoint for hydration. `{key}` substitutes from the host's other `data-*` attributes. |
| `data-lazy-tpl="name"`          | host  | Fragment name (no path, no `.html`). Helper fetches `/fragments/name`. |
| `data-lazy-trigger="open|click|now"` | host | When to fire. Default: `open` for `<details>`, `click` for everything else. `now` runs at attach time. |
| `data-lazy-submit="/path"`      | host  | Endpoint for `submit` action. URL substitution same as `-src`.  |
| `data-lazy-submit-method="PATCH"` | host | HTTP verb. Default `POST`.                                       |
| `data-lazy-after="render|close|event:<name>"` | host | Post-submit behaviour. Default `render`. |
| `data-lazy-action="submit|close|refresh"` | inside fragment | Verb for triggers (buttons inside the rendered template). |

### 4.3 Templating

Native `<template>` cloning. Two operations:

- **`{{key}}`** — HTML-escaped substitution. Dot paths (`{{foo.bar}}`).
  Empty / missing → empty string.
- **`{{#each items}}…{{/each}}`** — array iteration. Body is rendered
  once per item, with the item as the local context. `{{this}}`
  inside an `{{#each}}` over a primitive array refers to the item;
  otherwise dot paths resolve against the item.

That's it. No `{{#if}}` (use empty/non-empty strings at the data
layer), no `{{>partial}}` (one fragment = one file), no
unescaped output (`{{{raw}}}` deliberately omitted — if a feature
ever needs it we'll evaluate then).

Keep the parser tiny — under 50 lines of JS. Hand-rolled is fine.

### 4.4 URL substitution

Every `{key}` in `data-lazy-src` and `data-lazy-submit` resolves
against the host element's `data-*` attributes (kebab-cased lookup,
so `{tokenId}` reads `data-token-id`). Missing keys throw at attach
time, not at click time, so misconfigurations fail loud.

### 4.5 Submit payload

On `data-lazy-action="submit"`:

1. Collect every `[name]` input inside the host. Checkboxes group by
   name into an array (matches existing `selectedAbilitiesFromPicker`
   behaviour).
2. Merge with the host's `data-*` attributes (top-level only).
3. POST/PATCH the merged JSON to `data-lazy-submit`.
4. On success, follow `data-lazy-after`:
   - `render` (default) — re-run the template against the response body.
   - `close` — collapse the `<details>` (or hide the host for non-details).
   - `event:<name>` — `host.dispatchEvent(new CustomEvent(name, {bubbles: true, detail: response}))` so page code can listen.

---

## 5. Scope limits

These are **deliberately out of scope** for v1:

- **Progressive enhancement.** The helper assumes JS works. We're
  shipping an admin SPA; non-JS doesn't matter.
- **Swap modes.** Always replaces fragment contents inside the
  host. No `outerHTML` / `beforeend` / `afterend` strategies.
- **SSE / WebSockets.** The attack surface and test cost outweigh
  the value here.
- **Form validation.** Browser native validation only.
- **Animations / transitions.** CSS handles those.
- **Optimistic updates.** Wait for the response, render the canonical
  state. Predictable beats fast.

If a feature shows up that wants any of these, we adopt **HTMX**
(~10 KB minified) wholesale rather than grow GG.lazy into HTMX-shape.
The bar for new features in the helper itself is "we have a
concrete second caller AND it can't be solved with existing
attributes."

---

## 6. First migration target

**Abilities collapse on subscriptions cards.**

Why this one first:

- Already isolated in `installAbilitiesSection` (one function, one
  page).
- Exercises the helper end-to-end: fetch on open, render a list,
  submit a payload, re-render with the response.
- Has a real payload shape (checkbox group → array).
- Removing the imperative version is a clean diff to review.

Stage 1 (this slice): render the picker via GG.lazy using a
**programmatic data hook** (`bind(host, {getData})`) — the
abilities + `checked` flags are already in memory from the token
list response, and the bearer token can't safely ride in a URL
path. Save + dirty/clean tracking stays imperative; existing
PATCH `/api/admin/tokens` continues to handle saves.

Stage 2 (later, only if the helper proves out): switch the save
flow to `data-lazy-submit` + `data-lazy-after="render"`. The dirty
state tracking either lives as a thin behaviour on the rendered
button or moves to a small `GG.lazy.dirty` companion if a second
caller wants it.

---

## 7. Testing

- **Server fragments handler:** happy path (200 + ETag), unknown
  name (404), no admin session (401), `If-None-Match` round-trip
  (304). Plus a build-time check that every fragment file's
  `{{...}}` placeholders are referenced from at least one source
  file (catches typos / removed migrations).
- **Client GG.lazy:** none. We don't have a JS test runner and the
  user has been clear that the manual loop is sufficient for the
  admin UI. The helper is small enough that the contract review
  on this design doc + a careful first migration is the QA boundary.

---

## 8. Slice plan

- **Slice 1 — Helper + fragments + first migration.** ✅ shipped
  2026-05-02. Helper landed in `assets/lazy.js`, fragments served
  raw at `GET /fragments/{name}` (admin-gated, ETag-cached,
  gzipped at startup, 304 on revalidate). First migration was the
  abilities collapse on `/admin/subscriptions`.
- **Slice 2 — `data-lazy-submit` + abilities save flow.** ✅ shipped
  2026-05-02. `data-lazy-submit` + `data-lazy-submit-method` +
  `data-lazy-after` + `data-lazy-action="submit"` landed in
  `assets/lazy.js`. After-actions: `render` (re-render fragment
  against response — the default), `refresh` (re-run the read
  path), `close` (collapse the host or close the enclosing
  `.drawer`), `event:<name>` (dispatch a bubbling CustomEvent with
  `{request, response}` detail). Errors land in `[data-lazy-msg]`
  inside the rendered body and fire a `lazy-submit-error` event.
  Abilities save on `/admin/subscriptions` migrated as the first
  caller — the host's `data-token` rides as a body field (NOT a
  URL placeholder; subscription tokens are bearer creds), the
  abilities checkboxes collapse into the `abilities` array, and
  `event:abilities-saved` lets the page sync in-memory state +
  resync the summary chips. The `name="ability"` checkbox in the
  abilities fragment was renamed to `name="abilities"` so the
  helper's payload key matches the API contract directly.
- **Slice 3 — Migrate the rest opportunistically.** ✅ mostly
  shipped 2026-05-02. Token-card body, repo-card body, account
  detail row, repo-subscriptions collapse, and every drawer form
  body now render through GG.lazy from a fragment. The `/user`
  page got its own dedicated `user-subscription-card` fragment +
  a bespoke renderer (admin-shaped `renderTokenCard` didn't fit
  the read-only paste-three-values shape). The mirror-destination
  collapse on repos is the last imperative section that hasn't
  migrated; tracked as its own item in the README open work
  because of the three-state (no-dest / view / edit) shape.
