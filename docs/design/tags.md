# Tags

Status: **decisions resolved 2026-05-02**, no code yet — slicing to
start. Lightweight labels admins put on repos, subscription keys, and
accounts so the admin UI can be filtered, audited, and bulk-managed
by team / project / lifecycle without inventing a heavier "groups"
entity.

This doc is the source of truth for the data model, inheritance rule,
and admin surface; once approved it slices the same way
[`credential-vault.md`](credential-vault.md) and
[`accounts.md`](accounts.md) did — storage + API first, UI second,
filter/bulk-action UX last.

---

## 1. Why tags exist

A single GiGot deployment serves many teams (see
[`feedback_gigot_deployment_multi_team`](../../) framing in the
README §1 and `accounts.md` §2): each team gets its own repos, each
person on each team gets their own per-repo subscription key. That's
the right invariant — one key per user per repo, never one shared key
per team — but it produces a flat admin list that grows fast:

```
N teams × ~M repos/team × ~K humans/team = N·M·K subscription rows
```

At three teams, four repos each, ten humans each you're already at 120
subscription rows in the admin UI. Without a way to slice that list,
two operator workflows fall over:

1. **Filtering.** "Show me everything for the marketing team" — today
   you eyeball repo names and account names, hoping the convention held.
2. **Bulk revoke.** "ACME contracted us for a six-week project; revoke
   everything they had" — today that's a series of one-by-one DELETE
   clicks, easy to half-finish.

Tags are the smallest thing that makes both work. They are not a
permissions mechanism — auth still flows through roles and abilities
(`accounts.md` §2). Tags are organisational metadata only.

---

## 2. The model

Three things can carry tags: **repos**, **subscription keys**, and
**accounts** (humans). A tag is a plain string; the same string at
any of the three levels means the same thing (one shared vocabulary,
autocomplete-driven to fight drift).

**Effective tags on a subscription** =
`subscription.tags ∪ subscription.repo.tags ∪ subscription.account.tags`.
Inheritance is read-side only: the repo's and account's tags are not
copied onto subscriptions at write time, they're unioned at
query/display time. That means renaming or removing a repo tag (or an
account tag) instantly propagates to every relevant subscription —
no backfill, no drift between "tag was set when sub was issued" vs
"tag now."

```
Repo    "addresses"     tags: [team:marketing, env:prod]
Account "bob@acme.com"  tags: [contractor:acme]
└── Sub  bob@addresses  tags: [project:redesign]
    └── effective: [team:marketing, env:prod, contractor:acme, project:redesign]
                    ^from repo            ^from account     ^explicit on sub

Account "alice"         tags: []
└── Sub  alice@addresses tags: []
    └── effective: [team:marketing, env:prod]
                    ^from repo only — account adds nothing
```

The account-tagging case is the contractor lifecycle workflow: tag
`bob@acme.com` once as `contractor:acme`, every key Bob ever holds
inherits it automatically — today's keys, next week's keys, all of
them. When the contract ends, filter by `contractor:acme`, bulk-revoke,
done. No "remember to tag each new key" burden on the admin.

That's the whole model. Not key:value pairs at the schema layer — the
`team:marketing` colon convention is purely a UI/operator habit.
Storing flat strings keeps the schema simple and lets us layer faceted
filters on top later if usage proves them out (see §9).

---

## 3. Storage — four relational tables

JSON arrays / comma-strings are tempting because the migration's
shorter today, but they are exactly the wrong primitive for tags:
renames don't propagate, indexed filters become JSON-parse scans, and
audit columns have nowhere to live. Tags only earn their keep if they
are rename-safe and indexable from day one, which means a relational
shape:

```sql
CREATE TABLE tags (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  created_at   TIMESTAMP NOT NULL,
  created_by   INTEGER NOT NULL,            -- account.id of admin
  FOREIGN KEY (created_by) REFERENCES accounts(id),
  UNIQUE (name COLLATE NOCASE)              -- see §8 case rules
);

CREATE TABLE repo_tags (
  repo_id      INTEGER NOT NULL REFERENCES repos(id)  ON DELETE CASCADE,
  tag_id       INTEGER NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
  tagged_at    TIMESTAMP NOT NULL,
  tagged_by    INTEGER NOT NULL REFERENCES accounts(id),
  PRIMARY KEY (repo_id, tag_id)
);

CREATE TABLE subscription_tags (
  subscription_id INTEGER NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  tag_id          INTEGER NOT NULL REFERENCES tags(id)          ON DELETE CASCADE,
  tagged_at       TIMESTAMP NOT NULL,
  tagged_by       INTEGER NOT NULL REFERENCES accounts(id),
  PRIMARY KEY (subscription_id, tag_id)
);

CREATE TABLE account_tags (
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  tag_id       INTEGER NOT NULL REFERENCES tags(id)     ON DELETE CASCADE,
  tagged_at    TIMESTAMP NOT NULL,
  tagged_by    INTEGER NOT NULL REFERENCES accounts(id),
  PRIMARY KEY (account_id, tag_id)
);
```

(Schema is illustrative — GiGot's stores are sealed-file based today,
not SQL, but the shape carries cleanly into either.)

What the relational model buys over an array column:

- **Rename** is one row update in `tags`, propagates everywhere.
- **Effective tags** is `SELECT name FROM tags WHERE id IN (... UNION ...)`
  across all three join tables — indexable, no per-row JSON parsing
  in the hot path.
- **Bulk revoke by tag** is a clean set query — directly tagged subs,
  subs that inherit through `repo_tags`, and subs that inherit through
  `account_tags`, all in one statement.
- **Tag lifecycle is explicit** — removing a tag from a repo is a row
  in `repo_tags`, deleting the tag itself is a separate admin action
  in `tags` (see §6 deletion semantics).
- **Audit** carries on each join row (`tagged_at`, `tagged_by`) without
  retrofitting later.

---

## 4. Auth

Writes (create tag, assign tag, remove tag, delete tag) are
**admin-only**, session-gated, same posture as credentials and account
management. Maintainers do not write tags — a maintainer is a role
that allows holding `mirror`-bearing subscription keys and managing
their own repos' destinations (`accounts.md` §2), not an issuing role.
Regular accounts neither read nor write tag mutations.

Reads — i.e. seeing the tag pills on a repo or a subscription
— ride along with the existing repo and subscription detail responses
that maintainers and regulars already have access to. There is no
separate `GET /api/tags` for non-admins; if you can see the entity,
you can see its tags.

---

## 5. What the admin sees

A fourth entry in the admin sidebar, below "Credentials":

```
Repositories
Subscription keys
Credentials
Tags
```

### 5.1 Tags page

A flat catalogue of every tag in the system, sortable, with usage counts:

| Name              | Repos | Subs | Created           | Created by | |
| ----------------- | ----- | ---- | ----------------- | ---------- | — |
| team:marketing    | 4     | 18   | 2026-04-01        | peter      | Rename · Delete |
| team:platform     | 6     | 22   | 2026-04-01        | peter      | Rename · Delete |
| contractor:acme   | 0     | 3    | 2026-04-15        | peter      | Rename · Delete |
| env:prod          | 9     | —    | 2026-03-20        | peter      | Rename · Delete |

Counts include direct assignments only (the `_tags` join rows), not
inherited references — admins want to know "how many places did
*I* explicitly put this," and the inheritance answer is derivable from
that.

`Add tag` form above the list: just a `Name` field. New tags are also
created implicitly when an admin types a not-yet-known string into a
tag picker on a repo or subscription detail page (§5.2 / §5.3); both
paths converge on the same `tags` row.

### 5.2 Repo detail — Tags section

A pill cluster above the existing Destinations panel:

```
Tags  [team:marketing] [env:prod]  [+ add tag ▾]
```

The picker is autocomplete-backed by the global `tags` catalogue. Typing
a known tag selects it; typing a new string and pressing Enter creates
the tag *and* assigns it. Click an `×` on a pill to remove the assignment
(the tag itself stays in the catalogue).

### 5.3 Account detail — Tags section

Same pill cluster, same picker, on the admin's account detail page.
Tags assigned here propagate to every subscription that account holds
(see §2 effective formula, §7 inheritance rules). Removing an account
tag instantly drops it from every key the account owns — useful at
contract-end, painful if mis-clicked, so the picker confirms before
removal of any tag with downstream effect.

### 5.4 Subscription detail — Tags section

Same pill cluster, same picker. The display distinguishes inherited
from explicit pills, and labels each inherited pill with its source so
the admin sees *why* it's there:

```
Tags  [team:marketing ↩ from repo]
      [env:prod ↩ from repo]
      [contractor:acme 👤 from bob@acme.com]
      [project:redesign ×]
                                       ^explicit
```

- **Inherited pills** are muted (`--modal-highlight-bg` background)
  and carry a small source label — `↩ from repo` or
  `👤 from <account>` — so the admin can trace the assignment back to
  its origin in one click. They have no `×` button on this page.
- **Explicit pills** use the standard pill styling with an `×` button
  to remove just this assignment.

This split is the most important UX detail in the whole feature: an
admin who tries to remove an inherited pill expects the tag to
disappear from this subscription only. By making the inherited ones
non-removable on the subscription page — and labelling their source —
we steer the action to the right scope (untag the repo, or untag the
account, depending on where it came from).

### 5.5 Subscription list filter — grouped chips

The existing `/admin/tokens` page grows a tag-chip filter row above
the table. Chips are **grouped by prefix-before-colon**: tags like
`team:marketing` and `team:platform` cluster under a "Team" heading;
`env:prod` and `env:staging` under "Env"; `contractor:acme` under
"Contractor". Tags with no colon prefix sit under a generic "Other"
group at the end.

```
Filter:  Team:        [marketing] [platform] [design]
         Env:         [prod] [staging]
         Contractor:  [acme] [bigco]
         Project:     [redesign]
         Other:       [legacy] [archived]
```

Selecting one or more chips filters by the **effective** tag set, so
`team:marketing` finds directly tagged subs, subs that inherit it
through their repo, and subs that inherit it through their account.
Multiple chips intersect (AND).

Grouping is purely a display affordance — the schema stores plain
strings (§8) and tags without colons sort cleanly into "Other"
without needing special handling.

### 5.6 Bulk revoke by tag

Below the chip filter, when one or more chips are active, a destructive
action button: **`Revoke all matching (N)`**. Clicking it shows a
confirm dialog that lists every subscription about to be deleted (name,
repo, account, abilities) and requires a typed-confirmation phrase
before firing — same pattern we use for irreversible admin actions
elsewhere. One click + one confirm sweeps the filtered set; nothing
implicit, nothing silent.

---

## 6. API surface

Admin-only, session-gated. Same shape as `/api/admin/credentials` and
`/api/admin/repos/{name}/destinations`.

### 6.1 Tag catalogue

| Method | Path                       | What it does |
| ------ | -------------------------- | ------------ |
| GET    | `/api/admin/tags`          | List every tag with usage counts. |
| POST   | `/api/admin/tags`          | Create a tag. Body: `{name}`. 409 on duplicate (case-insensitive). |
| PATCH  | `/api/admin/tags/{name}`   | Rename. Body: `{name: "..."}`. 409 if the new name collides. |
| DELETE | `/api/admin/tags/{name}`   | Delete. Cascades through every join table — the tag and all its assignments (repo, subscription, account) disappear in one statement. Response body returns the per-table counts that were swept so the audit log and the UI confirmation both have the blast radius. |

The UI (§5.1) wraps `DELETE` in a confirm dialog that shows the counts
before firing — server-side cascade is the simple primitive, the
"are you sure" lives in the front end where typos happen.

### 6.2 Assigning tags

Per-repo, per-subscription, and per-account, fanning out from the
entity that owns the assignment:

| Method | Path                                           | What it does |
| ------ | ---------------------------------------------- | ------------ |
| GET    | `/api/admin/repos/{name}/tags`                 | List tags directly assigned to this repo. |
| PUT    | `/api/admin/repos/{name}/tags`                 | Replace the tag set. Body: `{tags: ["..."]}`. Creates unknown tags as a side effect. |
| POST   | `/api/admin/repos/{name}/tags/{tag}`           | Assign a single tag (idempotent). Creates the tag if unknown. |
| DELETE | `/api/admin/repos/{name}/tags/{tag}`           | Unassign (does not delete the tag from the catalogue). |

Subscription tags are managed via the **existing** `PATCH /api/admin/tokens` endpoint, not via a dedicated path:

| Method | Path                            | What it does |
| ------ | ------------------------------- | ------------ |
| PATCH  | `/api/admin/tokens`             | Body extends the existing repo + abilities patch with `tags *[]string`. When non-nil, the server diffs the new set against the sub's current explicit tags and emits one `tag.assigned.subscription` / `tag.unassigned.subscription` event per actually-changed assignment. The token rides in the body (matching the existing PATCH shape), never in the URL — so the bearer never lands in access logs / browser history. |

The token list response (`GET /api/admin/tokens`) gains two new fields per row: `tags` (the sub's direct tags) and `effective_tags` (the §2 union — `sub.tags ∪ repo.tags ∪ account.tags`). Inherited tags are read-only on the subscription side; to remove one, untag the parent repo or account.

| GET    | `/api/admin/accounts/{id}/tags`                | List tags directly assigned to this account. |
| PUT    | `/api/admin/accounts/{id}/tags`                | Replace the tag set. Body: `{tags: ["..."]}`. Creates unknown tags as a side effect. Every subscription this account holds picks up the change immediately. |
| POST   | `/api/admin/accounts/{id}/tags/{tag}`          | Assign a single tag (idempotent). |
| DELETE | `/api/admin/accounts/{id}/tags/{tag}`          | Unassign. Same blast-radius confirmation pattern as §5.3 — the UI shows downstream subscription count before firing. |

### 6.3 Filtered list + bulk revoke

Existing list endpoints grow `?tag=` repeating query params filtering
by effective tags (AND across multiple values):

```
GET /api/admin/subscriptions?tag=team:marketing&tag=env:prod
```

Plus one new bulk endpoint:

```
POST /api/admin/subscriptions/revoke-by-tag
Body: {tags: ["team:marketing", "env:prod"], confirm: "<typed phrase>"}
Response: {revoked: [{id, account, repo, abilities}, ...], count: N}
```

The confirm-phrase requirement is server-side, not just UI — a buggy
admin script can't accidentally fire it.

---

## 7. Inheritance, in detail

The rule is `effective(sub) = sub.tags ∪ sub.repo.tags ∪ sub.account.tags`,
with a few edge cases worth pinning:

- **Removing a repo or account tag** — every subscription that
  inherited it stops showing it on the next read. No backfill, no
  notification. The same is true in reverse: adding a tag to a repo
  or account immediately surfaces it on every subscription that
  inherits.
- **Same tag from two sources** — if a sub's repo carries
  `team:marketing` *and* its account carries `team:marketing`, the
  effective set has it once (set union, not multiset). The
  subscription detail page picks one source to attribute (repo
  wins by convention, since repo membership is the more stable
  signal) and surfaces a tooltip noting both sources are tagged.
- **Filter precedence** — when an admin filters by `team:marketing`,
  inherited and explicit matches are equivalent at the list-row
  level. The pill styling distinction only kicks in inside the
  subscription detail page.
- **Bulk revoke matches inherited tags too** — this is the powerful
  case and the dangerous one. Revoking everything tagged
  `contractor:acme` sweeps subs tagged directly, subs whose repo
  carries the tag, *and* subs whose account carries the tag. The
  confirm dialog (§5.6) lists every match with its source(s) so the
  admin can see why each row is included before firing.
- **A subscription on a repo with no tags and an account with no
  tags** has effective tags equal to its explicit tags. The set
  just collapses; nothing special.

---

## 7.1 Audit trail — every action

Every tag-related admin action emits an audit event. The destination
depends on whether the event has a clear repo target:

- **Per-repo events** (anything that changes the tag set on a specific
  repo or on a subscription against a specific repo) ride on that
  repo's existing `refs/audit/main` chain — the same audit ref already
  used for repo lifecycle, mirror events, etc.
- **System-wide events** (anything that doesn't belong to one repo —
  catalogue lifecycle and account-level assignments, since accounts
  span repos) land in a new sealed system audit log:
  `data/audit_system.enc`. Same NaCl-box sealing as the other stores;
  rewrapped by `-rotate-keys` alongside the rest. Append-only writes,
  read via a new admin endpoint `/api/admin/audit/system`.

This split keeps repo-bound events forensically tied to their repo
(so a `git fetch refs/audit/main` still tells the whole repo story),
while server-wide events have a single destination instead of being
fanned out noisily to every repo.

Event-type table with destinations:

| Event type                    | Destination                                  | Fired by |
| ----------------------------- | -------------------------------------------- | -------- |
| `tag.created`                 | `audit_system.enc`                           | `POST /api/admin/tags` |
| `tag.renamed`                 | `audit_system.enc` — payload: old + new      | `PATCH /api/admin/tags/{name}` |
| `tag.deleted`                 | `audit_system.enc` — payload: swept counts (repos/subs/accounts) | `DELETE /api/admin/tags/{name}` |
| `tag.assigned.repo`           | repo's `refs/audit/main`                     | `POST /api/admin/repos/{name}/tags/{tag}` (and via PUT diff) |
| `tag.unassigned.repo`         | repo's `refs/audit/main`                     | `DELETE /api/admin/repos/{name}/tags/{tag}` (and via PUT diff) |
| `tag.assigned.subscription`   | sub's repo's `refs/audit/main`               | `PATCH /api/admin/tokens` (body diff on `tags`) |
| `tag.unassigned.subscription` | sub's repo's `refs/audit/main`               | `PATCH /api/admin/tokens` (body diff on `tags`) |
| `tag.assigned.account`        | `audit_system.enc`                           | `POST /api/admin/accounts/{id}/tags/{tag}` (and via PUT diff) |
| `tag.unassigned.account`      | `audit_system.enc`                           | `DELETE /api/admin/accounts/{id}/tags/{tag}` (and via PUT diff) |

PUT (replace-the-set) emits one event per actually-changed assignment,
not one per call — diffing the new set against the existing one keeps
the audit chain readable instead of "PUT replaced 18 tags" in a single
opaque row.

The `tag.deleted` event is the most load-bearing of the lifecycle
events because cascade delete (Q1) sweeps assignments without firing
individual unassignment events; the `tag.deleted` payload carries the
full sweep so a forensic reader can still answer "did sub-77 ever
carry `team:marketing`?" by replaying the chain.

---

## 8. Naming, casing, normalisation

- **Case-insensitive uniqueness, display preserves first-seen casing.**
  `team:marketing` and `Team:Marketing` collide on insert; the canonical
  display is whatever the *first* admin to create that tag typed.
  Renaming through `PATCH /api/admin/tags/{name}` is the only way to
  change the display casing afterwards.
- **Trim whitespace** on insert, reject empty, reject names longer than
  64 characters. Reject characters that would break a URL path segment
  (`/`, `?`, `#`, control chars) so the path-style API surface stays
  clean.
- **No structured key:value at the schema layer.** The `team:marketing`
  / `env:prod` colon style is a UI convention, not a constraint. Storing
  flat strings keeps us free to layer faceted filtering on top later
  (group sidebar by `team:`, `env:`, `contractor:` prefix) without a
  schema migration. If usage shows the convention sticks, we promote
  it to a UI affordance; if it doesn't, no harm done.

---

## 9. Resolved decisions (2026-05-02)

The four open questions from the original proposal closed with these
answers — kept here as rationale for future readers.

- **Tag deletion: cascade by default.** `DELETE /api/admin/tags/{name}`
  sweeps every assignment in `repo_tags`, `subscription_tags`, and
  `account_tags` along with the tag itself. The earlier proposal of
  refuse-with-count was rejected as friction without payoff — tags
  are organisational metadata, not load-bearing config; the worst
  case is a label disappearing from some pills, not a broken push.
  The UI confirm dialog (§5.1) shows the sweep counts before firing,
  which covers the "see what you're about to break" instinct without
  forcing a multi-step API dance.
- **Filter chips: grouped by prefix.** §5.5's chip layout groups
  `team:*`, `env:*`, `contractor:*`, etc. under headings from day
  one, with "Other" for tags that have no colon. The earlier worry
  about convention drift between writers doesn't apply here because
  tag writes are admin-only (§4) — one consistent author per
  deployment, the colon convention will hold.
- **Audit everything.** Every tag action — create, rename, delete,
  assign, unassign, across repos/subs/accounts — writes an event to
  `refs/audit/main`. See §7.1 for the full event-type table. The
  earlier "lifecycle only" proposal was rejected; full audit was
  preferred even at the cost of chain noise, because tag-driven
  bulk revoke is a destructive operation and the per-assignment
  history is exactly what a forensic reader will want six months
  later.
- **Account tagging is in v1.** Three things carry tags: repos,
  subscriptions, *and* accounts (§2). The earlier two-entity model
  was rejected because the contractor lifecycle case is concrete
  enough to design for now — tagging `bob@acme.com` once and having
  every key Bob holds inherit `contractor:acme` is materially
  better than per-key tag bookkeeping at contract end.

---

## 10. Slice plan

Three slices, each independently shippable:

- **Slice 1 — Storage + admin API + Tags page.** Four tables (tags,
  repo_tags, subscription_tags, account_tags). Endpoints from §6.1
  only. Tags page with create/list/rename/cascade-delete. Audit-trail
  events for `tag.created` / `tag.renamed` / `tag.deleted` (§7.1)
  wired here — assignment events come with slice 2 since the
  endpoints don't exist yet. No assignment UI on repos / subs /
  accounts in this slice.
- **Slice 2 — Repo + subscription + account assignment UI.** Endpoints
  from §6.2 (all three blocks: repos, subscriptions, accounts). Pill
  clusters on the three detail pages, with the inherited-vs-explicit
  visual distinction (§5.4) and source labelling (`↩ from repo`,
  `👤 from <account>`). Effective-tags computation lands here (read-side
  three-way union in the detail responses). Six new audit events
  (`tag.assigned.{repo,subscription,account}` and the matching
  unassigned variants, §7.1).
- **Slice 3 — Grouped chip filter + bulk revoke.** §6.3 endpoints.
  Prefix-grouped chip filter (§5.5) + `Revoke all matching` action on
  the subscription list, with the enumerate-then-confirm dialog and
  the typed-confirmation phrase gate.

---

## 11. Decision checklist

All four resolved 2026-05-02 — see §9 for rationale.

- [x] Tag deletion semantics — **cascade by default**, UI confirm
      dialog shows the sweep counts before firing.
- [x] Filter UX — **chips grouped by prefix-before-colon** from day
      one; `team:*` / `env:*` / `contractor:*` cluster under their
      respective headings, no-colon tags fall under "Other".
- [x] Audit trail — **every tag action writes to `refs/audit/main`**,
      including per-assignment churn. See §7.1 for the event-type
      table.
- [x] Account tagging — **in v1**, three things carry tags: repos,
      subscriptions, accounts. Effective set on a subscription unions
      across all three.
