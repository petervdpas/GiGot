# Credential vault

Status: proposed. A generic place to store secrets the server uses on
your behalf when it talks to outside systems (GitHub, Azure DevOps,
Gitea, anything future). Written UI-first, because the point of the
vault is that a normal admin can manage it without knowing the
plumbing.

---

## 1. The chain

The whole access model reads as one clean chain, top-to-bottom:

```
Subscription key  ──grants access to──▶  Repository  ──mirrors via──▶  Credential
    (who)                                  (what)                        (where)
```

- A **subscription key** says *who* is allowed in (which Formidable
  client/device). It already carries a repo allowlist today.
- A **repository** is the thing being synced. New in this design:
  a repo also carries a list of outbound destinations, each one
  pointing at a credential by name.
- A **credential** says *where* to mirror to — a URL + the secret
  GiGot needs to push there.

So when a Formidable client pushes: the subscription key lets them in,
the repo accepts the push, then for each attached destination GiGot
looks up the named credential in the vault and mirrors outward. One
chain, three layers, each layer only knows about the one below it.

A credential can be reused across many repos (your GitHub PAT probably
works for all of them). A repo can have many destinations (GitHub *and*
Azure DevOps *and* Gitea). That's the many-to-many wiring the chain
allows.

---

## 2. What it's for

GiGot has three kinds of secrets already:

1. **Admin passwords** — for humans logging into `/admin`.
2. **Subscription keys** — for Formidable clients talking to GiGot.
3. **Enrolled client public keys** — for sealing responses.

None of those cover the case of **GiGot talking outward** — pushing
a mirror to GitHub, pulling from a corporate Gitea, whatever comes
next. The credential vault is the fourth slot: secrets GiGot itself
carries to authenticate to someone else.

Built as a standalone feature so any future outbound integration
(mirror sync first, others later) just asks the vault for the right
credential by name instead of inventing its own storage.

---

## 3. What the admin sees

A third entry in the sidebar, below "Subscription keys":

```
Repositories
Subscription keys
Credentials
```

Clicking it shows a list:

| Name              | Kind                  | Expires      | Last used         |  |
| ----------------- | --------------------- | ------------ | ----------------- | — |
| github-personal   | Personal access token | 2026-09-01   | 2 hours ago       | Delete |
| azdo-work         | Personal access token | —            | never             | Delete |
| gitea-self-host   | Username + password   | —            | 5 days ago        | Delete |

Above the list, an **Add credential** form:

- **Name** — free text, what the admin calls this credential ("github-personal")
- **Kind** — dropdown: *Personal access token*, *Username + password*, *SSH key*
- **Secret** — write-only input (shown as password field, masked). For
  username+password kinds, two fields; for SSH key, a textarea.
- **Expires** — optional date. If set, the list shows a warning when
  it's within 7 days of expiring.
- **Notes** — optional free-text line, e.g. "work laptop, rotate
  quarterly."

That's all. The admin never sees the raw secret again after saving —
the list only shows metadata.

---

## 4. What the admin does NOT see

- Where on disk it's stored.
- How it's encrypted.
- NaCl / sealed files / key derivation.
- Whether credentials are sealed per-admin or server-wide.

Those live in the implementation. The UI stays in plain terms.

---

## 5. Linking credentials to repos

The repo is the owner of the link. Each repo carries a list of
destinations:

```
Repo "addresses" → destinations: [
  { url: "https://github.com/alice/addresses.git",       credential: "github-personal", enabled: true  },
  { url: "https://dev.azure.com/corp/_git/addresses",    credential: "azdo-work",       enabled: true  },
  { url: "https://gitea.example.com/alice/addresses.git", credential: "gitea-self-host", enabled: false },
]
```

The repo points at a credential **by name**, not by value. That gives
you rotation for free: change the secret in the vault once, every
repo that references it picks up the new value on the next push. No
per-repo re-configuration.

In the admin UI, the repo detail page gains a "Destinations" section:
a small table of URL + which credential + enabled, with add/remove
buttons. Adding a destination shows a dropdown populated from the
vault — no free-text secret entry here, which means secrets never get
pasted twice and never live in two places.

Deleting a credential from the vault is blocked with a 409 if any
repo still references it. The UI names the repos so the admin can
either retarget those destinations first, or delete them.

---

## 6. API surface

Admin-only, session-gated. Same shape as the existing
`/api/admin/tokens` endpoints.

| Method | Path                                | What it does                          |
| ------ | ----------------------------------- | ------------------------------------- |
| GET    | `/api/admin/credentials`            | List — name/kind/expires/last-used only. Never the secret. |
| POST   | `/api/admin/credentials`            | Create. Body carries the secret; response is metadata only. |
| PATCH  | `/api/admin/credentials/{name}`     | Update metadata or rotate the secret. |
| DELETE | `/api/admin/credentials/{name}`     | Delete. 409 if a repo still references it. |

The secret never crosses the wire in a GET response, ever. If an admin
forgets what the secret was, they rotate it — they don't retrieve it.

---

Repo-side destination endpoints (all admin-only, session-gated):

| Method | Path                                           | What it does                    |
| ------ | ---------------------------------------------- | ------------------------------- |
| GET    | `/api/admin/repos/{name}/destinations`         | List destinations on a repo.    |
| POST   | `/api/admin/repos/{name}/destinations`         | Add `{url, credential, enabled}`. 400 if the credential name doesn't exist in the vault. |
| PATCH  | `/api/admin/repos/{name}/destinations/{id}`    | Edit URL, swap credential, toggle enabled. |
| DELETE | `/api/admin/repos/{name}/destinations/{id}`    | Remove a destination.           |

## 7. Storage

New sealed file `data/credentials.enc` next to the existing sealed
stores. Same NaCl-box sealing to the server keypair. Same rotation
story — `./gigot -rotate-keys` re-wraps this file alongside
`tokens.enc`, `clients.enc`, `admins.enc`.

Each entry on disk:

```json
{
  "name": "github-personal",
  "kind": "pat",
  "secret": "<opaque bytes>",
  "expires": "2026-09-01T00:00:00Z",
  "last_used": "2026-04-19T12:04:11Z",
  "created": "2026-01-10T09:00:00Z",
  "notes": "work laptop, rotate quarterly"
}
```

No special-case encryption per-credential. The server key protects the
whole file; anyone with that key sees every credential, same as every
other sealed store. That matches the existing threat model — don't
invent a new one.

---

Repo destinations live alongside the repo's existing config (not in
the credential vault — the vault only holds secrets). A new sealed
file `data/destinations.enc` indexed by repo name, or a field on the
existing per-repo metadata. Either works; exact placement is an
implementation detail.

## 8. Open questions

- **Scope.** One vault per server, shared across all repos — or
  per-repo vaults? Per-server is simpler; per-repo is more
  defence-in-depth. Proposed default: per-server, because that
  matches how admins actually think ("my GitHub token").
- **Multiple admins.** If two admins exist, they share the vault.
  Is that right? Probably yes for v1; admin separation isn't a
  feature today.
- **Expiry enforcement.** When a credential is expired, should we
  refuse to use it (blocking the integration) or use it anyway with
  a warning? Proposed: warn in UI, but let the outbound call fail on
  its own. Don't double-gate.
- **"Last used".** Worth the bookkeeping? Yes — rotating credentials
  nobody uses is the first thing admins want to do. Cheap to update
  on every successful outbound call.

---

## 9. Decision checklist

Before starting:

- [ ] Is the sidebar really the right place, or does this belong
      under Subscription Keys as a sibling concept?
- [ ] Do we want the "kinds" dropdown to be an enum or free text?
      Enum is safer today; free text is easier to extend. Probably
      enum with an "other" fallback.
- [ ] Should we block `DELETE` when a credential is referenced by a
      repo, or allow deletion and flag the broken references? Block
      is safer.
