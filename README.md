# GiGot

<p align="center">
  <img src="docs/images/gigot.png" alt="GiGot" width="320">
</p>

Git-backed server for [Formidable](https://github.com/petervdpas/Formidable). GiGot
gives Formidable clients an optional, server-centered place to clone, push, and pull
templates and context while keeping Formidable itself local-first.

On first connect, a client receives a full clone of all templates and context.
After that, everything works locally with incremental sync back to GiGot via the
standard Git smart-HTTP protocol.

GiGot is designed to run in two very different deployment modes:

1. **Standalone.** A single binary, optionally fronted by your own reverse proxy,
   using its built-in authentication, encryption, and admin UI.
2. **Behind an API gateway** such as Azure API Management. In this mode the
   gateway handles TLS termination, subscription-key enforcement, rate-limiting,
   and identity, while GiGot focuses on serving Git.

A key feature in both modes is **application-layer end-to-end encryption of API
payloads** using NaCl box (curve25519 + XSalsa20 + Poly1305). Even when a gateway
terminates TLS and can see your HTTP traffic in the clear, it still cannot read
the sealed bodies of GiGot requests and responses.

---

## Table of Contents

0. [Roadmap / TODO](#roadmap--todo)
1. [Quick Start](#quick-start)
2. [Formidable-Context Scaffolding](#formidable-context-scaffolding)
2. [Command-Line Interface](#command-line-interface)
3. [Configuration Reference (`gigot.json`)](#configuration-reference-gigotjson)
4. [On-Disk Data Layout](#on-disk-data-layout)
5. [Authentication Overview](#authentication-overview)
6. [End-to-End Encrypted Bodies](#end-to-end-encrypted-bodies)
7. [Client Enrollment Flow](#client-enrollment-flow)
8. [Admin UI and Admin API](#admin-ui-and-admin-api)
9. [HTTP API Reference](#http-api-reference)
10. [Git Smart-HTTP Endpoints](#git-smart-http-endpoints)
11. [Deployment Modes](#deployment-modes)
12. [Security Model and Tradeoffs](#security-model-and-tradeoffs)
13. [Development and Testing](#development-and-testing)
14. [Project Structure](#project-structure)

---

## Roadmap / TODO

Open work, in rough priority order. This list mirrors the in-project task
tracker and is the source of truth for "what's next."

Mirror-sync and related work organizes into two deliberate tracks
(see [`remote-sync.md`](docs/design/remote-sync.md) §2.5): **Track A**
is GiGot's byte-level git mirror — disaster recovery, compliance,
git-to-git ecosystem — and is what the items below ship. **Track B**
is schema-aware publishing (records → Azure DevOps wiki, Confluence,
etc.) which explicitly belongs in Formidable's WikiWonder plugin, not
here. The items below do not overlap with Track B.

Done and shipping:

- [x] **Tags — slice 3 (design:
      [`tags.md`](docs/design/tags.md) §5.5, §5.6, §6.1, §6.3, §10).**
      Grouped chip filter clusters chips by prefix-before-colon
      (`team:*`, `env:*`, `contractor:*`, then "Other"); selecting
      one or more chips intersects (AND) so an admin filtering by
      `team:marketing` finds rows tagged directly + rows that
      inherit it. URL is the source of truth (`?tag=` repeating) so
      deep-links / copy-pasted URLs hydrate the filter on load.
      Filters now ship on **all three taggable pages**:
      `/admin/subscriptions` (server-side via
      `GET /api/admin/tokens?tag=` against the effective tag set —
      sub.tags ∪ repo.tags ∪ account.tags),
      `/admin/repositories`, and `/admin/accounts` (client-side
      narrowing of in-memory rows). One JS controller
      (`GG.tag_filter` in `assets/tag_filter.js`, symmetric to
      `GG.tag_picker`) backs all three; each page hands it `rows`,
      `rowTags`, and a `renderRows` callback, and the controller
      owns chip rendering, URL state, AND-filter computation, and
      stale-selection pruning (last assignment removed → chip drops
      off the URL and the row re-narrows automatically, no manual
      reload).
      `POST /api/admin/subscriptions/revoke-by-tag` revokes every
      effective-tag match in one call, gated by a deterministic
      typed-confirmation phrase
      (`revoke <comma-joined-lower-tags>`) checked **server-side**
      so a scripted caller can't bypass it. Each revoked sub emits a
      `tag.revoked.bulk` event on its repo's `refs/audit/main`. The
      chip filter card auto-hides the destructive button when no
      chip is selected or no row matches; confirm dialog enumerates
      every sub (account, repo, abilities) before firing.
      `POST /api/admin/tags/sweep-unused` bulk-removes catalogue
      rows with zero references; surfaced on `/admin/tags` as a
      "Remove unused" header button (auto-disabled when nothing is
      unused) so the catalogue stays tidy without a one-by-one
      delete loop. New semantic palette (`assets/theme.css`): tags
      green, repos orange, roles blue (`.badge[data-role="admin"]`
      etc., refactor from class proliferation), subscriptions
      purple — both light and dark variants. Swagger regenerated,
      integration scenarios + 12 new handler tests across the bulk
      revoke, listing filter, sweep, and store layers.
- [x] **Tags — slice 2 (design:
      [`tags.md`](docs/design/tags.md) §6.2, §7.1, §10).**
      Assignment surfaces wired across all three taggable entities:
      `PUT /api/admin/repos/{name}/tags`, `PUT /api/admin/accounts/{provider}/{identifier}/tags`,
      and the existing `PATCH /api/admin/tokens` body extended with
      `tags *[]string` for subscription assignments (token rides in
      the body, never in the URL — design §6.2 refinement). Each
      diff emits per-change audit events: repo + subscription
      events to that repo's `refs/audit/main`; account events to
      the system audit log. Token list / `/api/me` responses now
      carry both `tags` (direct) and `effective_tags` (the §2
      three-way union). Reusable `GG.tag_picker` (assets/tag_picker.js)
      mounted on each repo card, account row, and subscription card —
      explicit pills are removable, inherited pills render muted with
      a source label. Swagger regenerated, Cucumber coverage extended,
      handler + unit tests across each layer green.
- [x] **Tags — slice 1 (design:
      [`tags.md`](docs/design/tags.md) §3, §6.1, §10).**
      Storage scaffold for the four-table tag model: `tags` catalogue
      plus `repo_tags` / `subscription_tags` / `account_tags`
      assignment sets in a single sealed `data/tags.enc`.
      `internal/tags` package with Open/Put/Get/All/Rename/Remove
      and the four `*_tags` join helpers (write paths only used by
      the catalogue endpoints in this slice — assignment endpoints
      land in slice 2). Catalogue API at `/api/admin/tags`
      (GET/POST/PATCH/DELETE) with case-insensitive uniqueness +
      cascade-delete-by-default per design §11. New admin sidebar
      entry "Tags" + dedicated page (`/admin/tags`) listing the
      catalogue with create / rename / delete forms; usage counts
      per tag visible from day one. New system audit log
      `data/audit_system.enc` (sealed, rewrapped by `-rotate-keys`
      alongside the other `.enc` files) carrying tag.created /
      tag.renamed / tag.deleted events per §7.1; repo-bound and
      account-level assignment events come with slice 2. Swagger
      annotations + unit + handler + Cucumber coverage.

- [x] **Docker image — slice 3 (design:
      [`docker-image.md`](docs/design/docker-image.md) §8, §12).**
      New `publish-image` job in `.github/workflows/release.yml`
      gated on `needs: [test]` + `if: startsWith(github.ref,
      'refs/tags/v')`, so a failing test stops the image push the
      same way it already stops the binary release. Multi-arch
      build via `docker/setup-qemu-action@v3` +
      `docker/setup-buildx-action@v3` covering `linux/amd64`
      and `linux/arm64` (matches the binary matrix; arm/v7 +
      Windows containers explicitly out per design §10).
      `docker/login-action@v3` reads `DOCKERHUB_USERNAME` +
      `DOCKERHUB_TOKEN` repo secrets — operator-provisioned at
      `Settings → Secrets and variables → Actions`, never in the
      workflow file. `docker/metadata-action@v5` emits two tags
      per release: `petervdpas/gigot:<version>` (semver, leading
      `v` stripped — Docker Hub convention) and
      `petervdpas/gigot:latest`. `docker/build-push-action@v6`
      passes `provenance: true` + `sbom: true` so the published
      image carries supply-chain attestations — closes the
      "Missing supply chain attestation(s)" finding Docker Scout
      raised on the slice 1 / 2 hand-pushes. `VERSION` build-arg
      stays in lock-step with the tarball job's
      `-X main.appVersion=${VERSION}` ldflag so a `gigot
      -version` from the image matches a `gigot -version` from
      the tarball at the same tag. README §11.2 grows a tag
      table (`:latest` / `:X.Y.Z` / `:dev`) and the raw-Docker
      examples now pull `:latest` instead of the local
      `gigot:dev` build tag. Slice 3 only fires on the *next*
      tag push — the in-flight v0.4.0 run was triggered before
      slice 3 landed and produces the existing tarballs only.
      The full docker-image rollout (design doc §12 slices 1–3)
      is now shipped end-to-end; only the Kubernetes manifest
      stub and image-scanning CI integration remain on the
      "Later" list and neither blocks anything.

- [x] **Docker image — slice 2 (design:
      [`docker-image.md`](docs/design/docker-image.md) §7, §11, §12).**
      `docker-compose.yml` at the repo root captures the
      port + bind-mount + read-only-config layout so an operator
      doesn't reassemble it from the README every time.
      `image: petervdpas/gigot:dev` plus a `build: .` fallback
      means the same compose file works against the published
      image *and* against a local checkout. New `-healthcheck`
      one-shot flag (`internal/cli/healthcheck.go`,
      `ModeHealthcheck` in `cli.go`) probes
      `http://<server.host>:<server.port>/` with a 2 s timeout
      and exits 0/1; a `0.0.0.0` or `::` listen address is
      rewritten to `127.0.0.1` so the probe stays loopback-local
      regardless of the bind config. Dockerfile gains a
      `HEALTHCHECK --interval=30s --timeout=3s --start-period=10s
      --retries=3` directive that runs `/gigot -healthcheck`,
      since distroless has no `curl`/`wget` to call. Settings
      mirror design doc §7. Tests: parse-layer success +
      mutual-exclusion guard in `cli_test.go`, four
      `TestProbeHealth` cases in `healthcheck_test.go`
      (2xx success, 5xx surfacing the HTTP code, `0.0.0.0` →
      loopback rewrite, connection-refused). End-to-end smoke
      verified locally — `docker run` flips
      `.State.Health.Status` from `starting` → `healthy` after
      the start-period; `docker exec gigot /gigot -healthcheck …`
      exits 0 on the live listener and 1 with
      `dial tcp 127.0.0.1:<port>: connect: connection refused`
      against a dead port. Slice 2 also incidentally answered
      design doc §11's SIGTERM open question in slice 1: the
      server already drains cleanly on `docker stop`, so no
      handler change was needed. Slice 3 (release-workflow
      auto-publish on tag push) is the only remaining piece of
      the rollout.

- [x] **Docker image — slice 1 (design:
      [`docker-image.md`](docs/design/docker-image.md) §3, §12).**
      Multi-stage `Dockerfile` at repo root: `golang:1.25-alpine`
      build stage matching `release.yml`'s `-trimpath -ldflags
      "-s -w -X main.appVersion=${VERSION}"` flags exactly,
      `gcr.io/distroless/static-debian12:nonroot` runtime stage
      (uid 65532, ~23 MB final image). `EXPOSE 3417`,
      `WORKDIR /var/lib/gigot`, `ENTRYPOINT ["/gigot"]`,
      `CMD ["-config", "/etc/gigot/gigot.json"]`. `.dockerignore`
      excludes `data/` (sealed stores), `repos/` (private), local
      `gigot.json` (secrets), the host `gigot` binary, `.git/`,
      `.github/`, and editor noise so the build context stays lean
      and can't leak local secrets. README §11.2 documents the
      operator path: pre-`chown 65532:65532` the bind-mounted
      `data/` and `repos/` host dirs, `-add-admin` as a one-shot
      `-it` container against the same volumes, container-side
      defaults (`server.host=0.0.0.0`, `auth.enabled=true`)
      called out as the only deltas from the standalone config.
      No Go code changed; verified locally end-to-end (build →
      `-add-admin` via two-line piped stdin → server boot →
      `GET /` 200 / `GET /api/repos` 401 with auth on → `docker
      stop` triggers a clean `received terminated, shutting
      down... stopped cleanly` drain, which incidentally answers
      design doc §11's SIGTERM open question in the affirmative).
      `docker-compose.yml` + `-healthcheck` flag (slice 2) and
      the GHCR publish job in `release.yml` (slice 3) are
      explicitly deferred.

- [x] **Subscription keys are one-repo-per-key.** `TokenEntry.Repos
      []string` collapsed to `TokenEntry.Repo string` (required);
      `Issue(username, repo, abilities)` rejects an empty repo with
      `ErrRepoRequired` and rejects duplicates with
      `ErrDuplicateSubscription` — the store now enforces
      uniqueness on `(username, repo)`. Policy evaluator checks
      `entry.Repo == resource` directly; `filterReposForToken`
      reduces to a single-string match. Admin API
      (`POST/PATCH /api/admin/tokens`) takes a scalar `repo` field
      and returns 409 on duplicate. UI follows: the issue form
      uses a `GG.select` single-repo picker (no more multi-toggle
      cluster), cards render one repo chip and a de-duplicated
      header. Migration is fail-closed: `SealedTokenStore.LoadTokens`
      refuses to deserialize pre-migration entries with a "repos"
      list, naming the offending token so the admin can revoke +
      re-issue one key per repo before restarting. Demo setup
      mints two tokens (one per demo repo) and is idempotent
      across reruns. Tests: uniqueness + empty-repo unit tests on
      the strategy, fail-closed migration test on the store,
      409-on-duplicate feature scenarios at the HTTP boundary,
      updated repo_scope + policy + api_auth + me feature files.
      OpenAPI regenerated (`repo` on TokenRequest / TokenResponse /
      TokenListItem / UpdateTokenRequest). 2026-04-23.

- [x] **Accounts — Phase 6: maintainer role + role-vs-ability fences
      (design: [`accounts.md`](docs/design/accounts.md) §2, §6.1, §6.2).**
      Three-tier role model: added `maintainer` between `admin` and
      `regular`. Mirror-related endpoints
      (`/api/repos/{name}/destinations*`) now apply
      `requireMaintainerOrAdmin` *on top of* the existing
      `TokenAbilityPolicy("mirror")` check, so a stale `mirror` bit on
      a regular account's key fails at request time without needing a
      migration of old tokens. Issue-time fence parallel: `POST
      /api/auth/token` and `PATCH /api/admin/tokens` reject `mirror`
      on a regular's key with 400. Credentials and destination CRUD
      stay admin-only on the GiGot Repositories page (per §3.7's
      privacy-notice consent gesture and §6.2's single-source-of-
      truth rule); the maintainer-role subscriber surface is
      push-trigger only via `POST /destinations/{id}/sync`. The
      Subscription admin UI drops the chicken-and-egg
      `destination_count > 0` gate on the `mirror` ability picker —
      the role IS the structural fence, no bootstrap round-trip
      through the admin Repositories page. Accounts admin UI gains
      the maintainer option in the role dropdown, three "Make admin /
      maintainer / regular" row-menu items (current role hidden),
      and a teal `.badge.maintainer` style. Tests: positive +
      negative pairs for runtime role gate
      (`TestRepoDestinations_RegularRoleDenied`) and issue-time fence
      (`TestIssueToken_MirrorAbilityRequiresMaintainerOrAdmin`).

      **Email as first-class identity (2026-05-01).** `Account.Email`
      added as an independent field. GitHub identifier changed from
      `login` to **primary verified email** (fetched via
      `/user/emails` with the `user:email` scope; rejected if no
      verified primary). Microsoft consumer identifier changed from
      `sub` to **`email`** (consumer `sub` is unique per
      `(client_id, user)` per spec, so the same human signing into
      two App Registrations would create two account rows; email is
      stable across them). Entra unchanged (`oid` is already
      tenant-scoped + stable). OAuth callback writes Email +
      DisplayName on auto-register and *refreshes* them on every
      login (preserves Role, PasswordHash, CreatedAt — empty
      incoming claim treated as "IdP didn't send", not "user
      cleared"). New endpoint surfaces: `/api/me` returns `email`;
      `AccountView`, `CreateAccountRequest`, `UpdateAccountRequest`,
      `RegisterRequest` all carry `email` (lowercased + trimmed at
      the store boundary). Subscription chips on the Repositories
      page and token-card titles render `display_name` plus a muted
      email suffix so two accounts sharing a display name are
      distinguishable at a glance. Tests: GitHub negative paths
      (no primary verified email, only-unverified, /user/emails
      5xx + malformed JSON), OIDC email-populated-independently and
      missing-email-leaves-empty pairs, OAuth callback refresh +
      empty-claim-doesn't-clobber, HTTP-layer round-trip for create
      / patch / register / `/api/me`, plus three feature scenarios
      in `accounts.feature` and `me.feature` exercising the full
      session→handler→store path. 2026-05-01.

- [x] **Auth hot-swap admin surface (design:
      [`accounts.md`](docs/design/accounts.md) §9.5).** `/admin/auth`
      UI + `GET`/`PATCH /api/admin/auth` let an admin inspect and
      rewrite `allow_local`, every OAuth provider block, and the
      gateway block without a process restart. `Server.ReloadAuth`
      builds the candidate OAuth registry + gateway strategy
      outside a lock, swaps them in atomically on success, and
      persists the updated Auth block back to `gigot.json` (path
      remembered from `config.Load`; `Config.Path` is
      runtime-only, `json:"-"`). Any build failure —
      unresolvable `secret_ref`, unreachable discovery URL,
      empty `client_id` — short-circuits the reload and leaves
      the previous state untouched; the API surfaces the error
      verbatim. Response never leaks vault secret bytes (only the
      ref names). Sidebar gains an **Authentication** entry. Tests:
      `Provider.Replace/Remove` direct tests with concurrent
      Authenticate, `Registry.Replace/Remove` direct tests
      including nil-means-remove and stable provider ordering,
      `GET /api/admin/auth` snapshot contract (secret-leak guard),
      reload-semantics suite (enable/disable gateway, atomic
      rejection on bad secret_ref, allow_local flip live, session
      required on PATCH, persistence round-trip), plus three
      cucumber scenarios covering GET/PATCH happy path, flip +
      /admin/login 404, and bad-secret_ref rejection.

- [x] **Accounts + roles — Phase 4 gateway-trusted identity + Phase 5
      default-flip (design: [`accounts.md`](docs/design/accounts.md)
      §9–§10).** `internal/auth/gateway/` verifies an HMAC-SHA256
      signature over `<identifier>\n<timestamp>` keyed on a shared
      secret from the credential vault (`auth.gateway.secret_ref`).
      Three headers carry the claim (user / sig / timestamp); names
      are configurable so APIM-style deploys can point at their own
      `X-MS-CLIENT-PRINCIPAL-NAME`-ish convention. A replay window
      (`max_skew_seconds`, default 5 min) bounds a captured header's
      useful lifetime. Server-side bridge registers as an
      `auth.Strategy` after the session strategy so cookies still
      win; `requireAdminSession` now honours a gateway principal
      that resolves to a `role=admin` account, re-checking role per
      request so demotes take effect immediately. Boot warns when
      `allow_local=false` is combined with no non-local path or no
      non-local admin — the Phase-5 safety rail. Phase 5 is doc-only:
      the runtime `Defaults()` still ships `allow_local=true` (a silent
      flip in a minor version would lock upgraders out of their own
      server); operators flip it to `false` in `gigot.json` once
      OAuth or gateway is wired. Tests: `gateway.Verifier` unit
      suite (happy path, partial/missing headers, stale timestamp,
      malformed sig, tampered user, case-insensitive claim), plus
      server-level handler tests for admin access, regular-rejected,
      unknown-user-blocked vs. auto-registered, missing-headers
      fallthrough, and tampered-sig hard-reject.

- [x] **Accounts + roles — Phase 3 OAuth / OIDC + scoped token
      binding (design: [`accounts.md`](docs/design/accounts.md) §6, §8).**
      Redirect-flow login for three IdPs, all behind `go-oidc` + `golang.org/x/oauth2`
      with **no MSAL**. GitHub uses the OAuth2 flow plus a follow-up
      call to `api.github.com/user` (identifier = lowercased `login`).
      Entra uses OIDC against
      `https://login.microsoftonline.com/<tenant>/v2.0` (identifier =
      `oid`). Consumer Microsoft uses OIDC against the `consumers`
      audience (identifier = `sub`). Entra and Microsoft are kept as
      separate providers because the trust boundary (any MSA vs.
      a specific tenant) and the identifier shape differ — see
      `accounts.md` §2. Config block `auth.oauth.{github,entra,microsoft}`
      with `client_id`, `client_secret_ref` (resolved from the
      existing credential vault — no secrets in the config file),
      `tenant_id` (entra only), `allow_register` (auto-create
      `role=regular` on first callback; `false` rejects with a
      landing page pointing at an admin). Two endpoints per provider:
      `/admin/login/<name>` (GET; mints state + nonce + PKCE S256
      challenge, redirects to the IdP) and
      `/admin/login/<name>/callback` (GET; consumes the one-shot
      state, runs the token exchange, verifies the ID token and
      nonce, resolves the account, mints the same session cookie the
      local path uses). State store is in-memory with a 10-minute
      TTL and sweeping on write — bounded implicitly. Login page
      grows a "Sign in with <provider>" button per enabled provider
      via the public `/api/admin/providers` endpoint. Tests: state
      store TTL + one-shot, PKCE S256 format, OIDC round-trip
      against an in-process mock IdP (discovery, JWKS, RS256 ID
      token), GitHub two-hop (token + /user) against httptest stubs,
      nonce mismatch rejection, missing-claim rejection, and
      handler-level auto-register + session-cookie contract plus
      replay rejection. **Scoped token binding** (§6 follow-up):
      `POST /api/admin/tokens` and `POST /api/auth/token` now accept
      `"provider:identifier"` (e.g. `"github:petervdpas"`) as well as
      the bare back-compat shorthand, so OAuth accounts can actually
      hold subscription keys. `/admin/accounts` gains a
      "Subscriptions" column with a clickable count that jumps to
      `/admin/subscriptions?user=<scoped>`, which filters the grid
      and pre-selects the account in the issue form — which is now
      an account picker (`GG.select`) instead of a free-text box.
      Tests: `parseTokenUsername` table-driven (11 cases), two
      scoped-issuance scenarios (admin + api layer) + a scoped
      bind-rejection guard + a subscription-count cucumber.
      Retires the former "NaCl-challenge admin login" roadmap item.
- [x] **Accounts + roles — Phases 1 & 2 (design:
      [`accounts.md`](docs/design/accounts.md)).** One `Account` noun
      for every human, keyed by `(provider, identifier)` with closed
      `role ∈ {admin, regular}`. The sealed `admins.enc` auto-migrates
      into `accounts.enc` on first boot, `auth.allow_local` +
      `--allow-local` gate the local-password path, and `/admin/login`
      role-gates on `admin`. Subscription-token issuance binds to
      accounts — `POST /api/admin/tokens` now rejects unknown
      usernames with 400 (Phase 2; Phase 1 had a permissive
      auto-create, now retired). `POST /api/register` +
      `/admin/register` let anyone self-register a `regular` local
      account while `allow_local` is on. A new
      `/admin/accounts` console and full CRUD at
      `/api/admin/accounts[/{provider}/{identifier}]` let admins
      create, promote/demote, reset local passwords, and delete
      accounts — with server-side protection against removing the
      last admin (409). Tokens that predate the accounts model carry
      `has_account: false` in the list response and a **Bind to
      account** button on the subscriptions card fans out to
      `POST /api/admin/tokens/bind`, which creates the missing
      `regular` account so no token is left dangling. Phase 3 (OAuth
      / OIDC) is queued above.
- [x] **Credential vault — Expires field in the admin UI.** Closes the
      last UI gap on the vault (design doc §3). The
      `/admin/credentials` add form gained an optional
      `<input type="date" name="expires">`; the list table gained an
      `Expires` column between `Kind` and `Notes`. A small client-side
      classifier paints the cell red when the date is already past and
      amber when it's within 7 days, matching the design doc's
      "warning when a credential is within 7 days of expiring"
      language — it's advisory only, the server still never refuses to
      use an expired credential (see design doc §8 on the
      don't-double-gate decision). The wire shape didn't change:
      `CredentialView.Expires` was already declared and round-tripped
      by the store, so no Swagger regen was needed; the form now
      normalises the date input to UTC midnight before sending it. Tests:
      handler-level POST-with-expires + PATCH-expires round-trips in
      `handler_admin_credentials_test.go` (new file), plus a cucumber
      scenario in `integration/features/credentials.feature` that pins
      the create→list contract the UI classifier depends on.
- [x] **Mirror-sync — post-receive worker (slice 2b of mirror-sync).**
      Every accepted client push over `git-receive-pack` now
      automatically fans out to the repo's enabled destinations — no
      more manual Sync-now for the common case. New leaf
      `internal/server/mirror_worker.go`: buffered channel (depth
      128) + single worker goroutine per process; `enqueue` is
      non-blocking and drops with a log line when the queue is full
      so the receive-pack handler on the critical path of a user's
      `git push` can't stall on a backed-up mirror. Trigger: the
      existing audit step in `handler_git.go` now returns whether
      any ref moved; only then does the handler call
      `mirrorWorker.enqueue(repo)`. The worker fetches destinations
      per event (so enable/disable edits take effect on the very
      next push), skips `enabled=false`, skips destinations whose
      credential has been deleted out from under them (logged, not
      crashing), and calls the same `syncOnce` helper the manual
      Sync-now button uses — one push+record path, not two.
      One attempt per destination per trigger; a failed push lands
      in `last_sync_status=error` and the operator can retry via the
      Sync-now button. Retries + backoff + persistent queue are
      deliberately out of scope for 2b — queue entries are lost on
      restart, documented. Unit coverage in `mirror_worker_test.go`
      (enabled-only fan-out, success recording, failure recording,
      one-dest-failure-doesn't-starve-siblings, missing-credential
      skip, queue-full-is-non-blocking) and handler-level fences in
      `handler_git_test.go` (real `git push` enqueues exactly once,
      no-op push enqueues zero times). Design: `docs/design/remote-
      sync.md` §3.3–§3.4.
- [x] **Mirror destination — enabled toggle moved off the create/edit
      form.** The checkbox was noise (nobody adds a destination with
      `enabled=false`); it's gone. New destinations default to enabled
      server-side; edits omit the field entirely from the PATCH body
      so they inherit the current value. The enabled/disabled badge on
      the display row is now a click-to-toggle button — one click fires
      `PATCH /destinations/{id}` with `{"enabled": !current}` and
      refreshes the card in place. Pause/resume is a management gesture
      on an existing destination, not a new-thing form field. Pure
      admin.js + admin.css edit; the server API and tests were already
      PATCH-shaped so nothing needed touching on the Go side.
- [x] **Mirror-sync — admin UI (slice 3 of mirror-sync).** The
      existing mirror-destination section on each repo card grows
      three things: a last-sync status line ("never" /
      `ok <timestamp>` / `error <timestamp>` + collapsible stderr),
      a **Sync now** button that fires
      `POST /api/admin/repos/{name}/destinations/{id}/sync` and
      refreshes the card in place, and a prominent amber privacy
      warning + required consent checkbox on the add form per
      remote-sync.md §3.7 ("I understand the contents of this repo
      will be readable at the destination"). The checkbox is required
      on new destinations only — edits keep the original consent so
      a URL or credential tweak doesn't force a re-ack. Failed pushes
      show `last_sync_error` inline; successful pushes update
      `last_sync_at` and the operator sees the green badge without
      leaving the page. Stays on the existing 1:1-per-repo
      convention the card UI established — no multi-destination
      rewrite. No new Go code: `api.syncDestination`,
      `renderDestSyncBlock`, `formatSyncTime`, and a `.dest-privacy`
      CSS block are additive in `admin.js` / `admin.css`.
- [x] **Mirror-sync — manual sync endpoint (slice 2a of mirror-sync).**
      Track A first-fire. New `internal/server/mirror.go`
      `executeMirrorPush` shells out to
      `git push +refs/heads/*:refs/heads/* +refs/audit/*:refs/audit/*`
      using a one-shot `GIT_ASKPASS` shim so the credential secret
      never hits `/proc/*/cmdline` or the URL userinfo. Two
      synchronous routes call it:
      `POST /api/admin/repos/{name}/destinations/{id}/sync` (admin
      session) and `POST /api/repos/{name}/destinations/{id}/sync`
      (Bearer, gated by `TokenRepoPolicy` + `TokenAbilityPolicy("mirror")`).
      Both populate `last_sync_status` / `last_sync_at` /
      `last_sync_error`, redact the secret from captured output
      before storing, and call `credentials.Touch` on success.
      `enabled=false` destinations still accept a manual sync (the
      flag gates the automatic fan-out in slice 2b, not explicit
      operator action); a credential deleted out from under a
      destination returns **409** rather than crashing the push.
      `pushDest` is a swappable field on `Server` so tests stub the
      shell-out. Unit coverage in
      `internal/server/mirror_test.go` (real local bare-repo
      push, secret-leak regression, redactor) and
      `handler_sync_destination_test.go` (admin + subscriber
      success, failure status wiring, mirror-ability gate, disabled
      dest still syncs, missing credential 409, unknown id 404).
      Cucumber scenarios in `destinations.feature` lock the route
      existence + ability gate. Slice 2b (post-receive worker)
      reuses `executeMirrorPush` unchanged.
- [x] **Token abilities + `mirror` ability + subscriber-facing
      destinations API (slice 2.5 of mirror-sync).** `TokenEntry`
      grew an `abilities []string` field (additive, persisted in
      `tokens.enc`, rewrapped by `-rotate-keys`); `POST` and
      `PATCH /api/admin/tokens` accept an optional `abilities`
      array; the admin tokens UI grows a checkbox column that
      only shows a given ability when at least one credential
      exists (held-but-inert abilities still render as stale
      chips so admins can revoke them). First consumer is the
      `mirror` ability: new leaf `internal/policy.TokenAbilityPolicy`
      gates `GET/POST/PATCH/DELETE /api/repos/{name}/destinations`
      (Bearer auth) with `TokenRepoPolicy` AND
      `TokenAbilityPolicy("mirror")` — token-with-mirror allowed,
      token-without-mirror 403, out-of-scope repo 403, no-token
      401, and the admin override path `/api/admin/repos/{name}/destinations`
      is unchanged. Abilities are explicit claims attached to
      individual tokens (closest analogue: OAuth scopes), not a
      reintroduction of roles. Unit coverage in
      `internal/policy/policy_test.go`,
      `internal/server/handler_admin_tokens_test.go`, and
      `internal/server/handler_repo_destinations_test.go`;
      Cucumber scenarios in `destinations.feature` for
      mirror-allowed and mirror-denied. See
      [`remote-sync.md`](docs/design/remote-sync.md) §2.6.
- [x] **Refspec compatibility spike — GitHub accepts `refs/audit/*`.**
      Manual spike on 2026-04-20 against `petervdpas/Braindamage`
      with a fine-grained PAT (Contents R/W + Metadata R)
      confirmed GitHub accepts `+refs/audit/*:refs/audit/*` both
      in isolation and as part of the combined
      `+refs/heads/*:refs/heads/* +refs/audit/*:refs/audit/*`
      push. `git ls-remote` after the push showed both
      `refs/heads/master` and `refs/audit/main` on the remote.
      Gate-clears slices 2a and 2b — the audit chain travels with
      the mirror without a fallback. Azure DevOps not yet spiked;
      retest before claiming universal support. See
      [`remote-sync.md`](docs/design/remote-sync.md) §5.
- [x] **Basic auth narrowed to `/git/*` (defence in depth).** After
      adding Basic-auth support so git-over-HTTP works, the middleware
      initially accepted Basic on every bearer-gated route — more
      surface than needed. Tightened to match the Swagger spec's
      narrower claim: `Provider.MarkBasicPrefix` whitelists prefixes
      where Basic is accepted, server registers `/git/` as the only
      one. Outside those prefixes a Basic header gets a `401` +
      `WWW-Authenticate: Bearer realm="gigot"` so confused callers are
      told what scheme to use. The 401 challenge is path-aware — `/git/*`
      gets `Basic` (what git understands), `/api/*` gets `Bearer`.
      Unit coverage in `internal/auth/auth_test.go` is arranged as
      deliberate positive/negative pairs: Basic on `/git/` allowed vs.
      Basic on `/api/repos` rejected, Bearer on `/api/repos` allowed
      vs. per-path challenge scheme. Bearer acceptance is unchanged
      everywhere.
- [x] **Token auth accepts HTTP Basic (git-over-HTTP).** The README
      has always advertised
      `git clone http://user:<subscription-key>@host/git/repo` but
      until now `TokenStrategy.Authenticate` and `EntryFromRequest`
      only recognised `Authorization: Bearer ...`. Git sends
      `Authorization: Basic base64(user:token)`, so the auth
      middleware rejected every clone with "unauthorized" — the
      README lied. Fix: one `tokenFromRequest` helper accepts both
      schemes (username ignored on Basic, password is the token),
      shared by `Authenticate` and `EntryFromRequest` so the policy
      layer sees the same token allowlist regardless of scheme. The
      401 middleware response now sends
      `WWW-Authenticate: Basic realm="gigot"` so git (which holds off
      on credentials until it sees a challenge) retries with its
      stored token. Unit coverage in `internal/auth/token_test.go`
      (Basic-valid, Basic-invalid, Basic-empty-password, unknown
      scheme) plus `handler_git_test.go::TestGitCloneBasicAuthWithToken`
      which runs a real `git clone` against the httptest server with
      `auth.enabled=true`. A scope-check variant
      (`TestGitCloneBasicAuthWithUnscopedToken`) locks in that Basic
      support isn't a policy bypass — per-repo allowlists still
      apply.
- [x] **Convert-to-Formidable admin action.** New endpoint
      `POST /api/admin/repos/{name}/formidable` stamps
      `.formidable/context.json` on top of HEAD, flipping an existing
      plain repo into a Formidable context. Gated to
      `server.formidable_first=true` so generic-mode operators don't
      trip the feature accidentally. Idempotent: already-stamped
      repos return `stamped:false` with no new commit. Returns 422
      for empty repos (nothing to stamp on top of) with a hint to
      use `scaffold_formidable:true` at create time instead. Writes
      one `repo_convert_formidable` audit entry on successful stamp.
      Admin UI shows a "Convert to Formidable" button on non-Formidable,
      non-empty repo cards; handler delegates to the existing
      `stampFormidableMarker` helper so no duplicate stamp logic.
      `-add-demo-setup` now also provisions `postman-plain` (a non-
      Formidable companion to `postman-demo`) so the shipped
      GiGot-Formidable Postman collection has a conversion target
      without extra setup. Cucumber coverage in `formidable_first.feature`:
      convert plain repo (stamped + audit), convert already-Formidable
      (idempotent), generic-mode rejection (403), empty-repo (422),
      unauthenticated (401).
- [x] **Postman demo setup CLI.** `./gigot -add-demo-setup` provisions
      the exact state the shipped Postman collection expects — admin
      `demo` / password `demo-password`, scaffolded repo
      `postman-demo`, credential `postman-pat`, plus a fresh
      subscription token printed to stdout. `./gigot -remove-demo-setup`
      tears it back down, revoking every token ever issued to the
      demo user (repeat `-add` runs stack, so `-remove` sweeps all of
      them). Mutually exclusive with the rest of the one-shot family
      (`-init` / `-add-admin` / `-rotate-keys` / `-wipe-*` /
      `-factory-reset`). Re-running `-add-demo-setup` is idempotent
      on the admin/repo/credential and cumulative on tokens.
      Scaffolding moved from `internal/server/scaffold/` to the new
      leaf package `internal/scaffold/` so both the HTTP handler and
      the CLI seed the same embedded files — no drift. Unit coverage
      in `internal/cli/demo_test.go` asserts the provisioned state
      (admin bcrypt, scaffolded tree, credential kind, token in the
      sealed store) and the idempotent-remove contract. See the
      [Command-Line Interface](#command-line-interface) section for
      the flag reference and `docs/postman/` for the companion
      collection.
- [x] **Factory-reset / granular wipe CLI.** Seven granular
      `-wipe-*` one-shots (`-wipe-repos`, `-wipe-admins`,
      `-wipe-tokens`, `-wipe-clients`, `-wipe-sessions`,
      `-wipe-credentials`, `-wipe-destinations`) compose with each
      other; `-factory-reset` is the nuclear shorthand that also
      removes the keypair and rotation backups, restoring a clean-
      install state where only `gigot.json` survives. All destructive
      flags prompt for the literal word `yes` (bypass with `-yes` for
      scripts), treat missing paths as already-done so the operation
      is idempotent, and refuse to combine with `-init` /
      `-add-admin` / `-rotate-keys` (or each other, in the
      `-factory-reset`/granular mix). Planning is pure
      (`buildWipePlan`) so the prompt copy is exactly what
      `executeWipePlan` acts on. Unit coverage in
      `internal/cli/cli_test.go` (every parse + validation branch)
      and `internal/cli/wipe_test.go` (granular / repos-only /
      factory-reset / prompt refusal / prompt acceptance /
      idempotence / empty-targets refusal). CLI reference table in
      the [Command-Line Interface](#command-line-interface) section
      lists every flag.
- [x] **Audit trail — `git-receive-pack` event coverage (slice 3 of 3).**
      `handler_git.go` now snapshots `git for-each-ref` (excluding
      `refs/audit/*`) before the receive-pack subprocess runs, snapshots
      again on success, and appends one `push_received` audit entry per
      ref that actually moved — one per create/update/delete. The
      snapshot helpers live in `internal/git/refs.go`
      (`Manager.RefSnapshot` and pure `DiffRefSnapshots`). Receive-packs
      that reject every update (non-ff, hook refusal) produce an empty
      diff and so no audit noise. Unit coverage in
      `internal/git/refs_test.go` (create/update/delete diff + audit-ref
      exclusion), handler coverage in
      `internal/server/handler_git_test.go::TestGitPushEmitsPushReceivedAudit`
      (asserts ref, SHA, and `push_received` type on the top audit
      event), Cucumber scenario in `audit_trail.feature`
      ("A client push via smart-HTTP emits a push_received audit
      entry"). Combined with slices 1 and 2, the audit chain now covers
      every user-triggered write path: `repo_create`, `file_put`,
      `commit`, and `push_received`.
- [x] **Audit trail — tamper-proof guard (slice 2 of 3).**
      Every bare repo now carries a `hooks/pre-receive` that rejects any
      ref update under `refs/audit/*` from `git-receive-pack`. Installed
      by `InitBare`/`CloneBare` on new repos and retro-installed by
      `Manager.EnsureAuditGuards()` at server start on any legacy repo.
      Server-side writes via `AppendAudit` bypass hooks (they use
      `update-ref` directly), so the guard does not inhibit GiGot's
      own writes. Unit coverage in
      `internal/git/audit_guard_test.go` proves end-to-end push
      rejection: a forced client push of a forged commit to
      `refs/audit/main` is refused, and the ref still points at the
      server-written entry afterwards. Combined with the hash-chain
      tamper-evidence from slice 1, the audit chain is now both
      tamper-proof (cannot be overwritten by a client) and
      tamper-evident (any unauthorised modification changes every
      downstream SHA).
- [x] **Audit trail on `refs/audit/main` (slice 1).** Every repo carries
      a server-authored, append-only audit chain written as git commits
      on `refs/audit/main`. One entry per audited operation, chained by
      git's parent link (tamper-evident), authored and committed by
      `GiGot Audit <audit@gigot.local>` regardless of the actor.
      `internal/git.Manager.AppendAudit` is the sole writer, using
      `git hash-object` + `git mktree` + CAS `update-ref` with
      contention retry. Wired into `PUT /files`, `POST /commits`, and
      `POST /api/repos` success paths; event types so far are
      `file_put`, `commit`, and `repo_create`. Clients consume the
      chain via `git fetch refs/audit/main` — no new HTTP surface.
      Unit tests cover the CAS retry, parent chaining, and JSON
      roundtrip; Cucumber `audit_trail.feature` proves the ref advances
      by exactly one per wired operation and the top event's `type`
      matches. Slice 2 (`git-receive-pack` coverage + pre-receive hook
      to reject client writes to `refs/audit/*`) is listed under Open
      above. Design doc:
      [`docs/design/audit-trail.md`](docs/design/audit-trail.md).
- [x] **Persistent admin sessions.** Sessions now round-trip through
      `data/sessions.enc` (sealed, rewrapped by `-rotate-keys` alongside
      the other stores), so admins no longer re-login after every
      restart or key rotation. `auth.SessionStrategy.SetPersister`
      drops already-expired entries on load and scrubs them from disk
      so the file doesn't grow unbounded. Originally listed as
      "HA-friendly admin sessions" — relabeled because a file-backed
      store fixes *restart-survives* but not *multi-instance-shared
      state*; true HA still needs Redis/DB. Security-model writeup in
      [§Security Model and Tradeoffs](#security-model-and-tradeoffs)
      updated to reflect the reversed posture (session IDs now exist
      on disk, sealed).
- [x] **Mirror-sync — destinations data model + admin API (slice 1 of 3).**
      New `internal/destinations` package and sealed `data/destinations.enc`
      (rewrapped by `-rotate-keys`). Admin endpoints under
      `/api/admin/repos/{name}/destinations[/{id}]` support list / create /
      get / patch / delete, session-gated, Swagger-annotated. Creating or
      updating a destination rejects unknown `credential_name` with a 404
      against the vault; deleting a credential that is still referenced
      by any destination returns **409** with `{ ref_repos: [...] }`
      (credential-vault.md §5). Deleting a repo cascades — destinations
      under that name are dropped so they can't dangle. No push worker
      and no UI yet — those are slices 2 and 3. See
      [`docs/design/credential-vault.md`](docs/design/credential-vault.md) §5
      and [`docs/design/remote-sync.md`](docs/design/remote-sync.md) §3.1.
- [x] **Credential vault — storage + admin API + page (design §§1–4, §6, §7).**
      New sealed store `data/credentials.enc` (NaCl-boxed to the server
      pubkey, rewrapped by `-rotate-keys` alongside the other `.enc`
      files). `internal/credentials` owns Open/Put/Get/All/Remove/Touch;
      secrets never leave the server after write (`PublicView` + the
      handler's `credentialView` strip `Secret` on every response).
      `/admin/credentials` is a sibling page to the main admin SPA;
      endpoints under `/api/admin/credentials[/{name}]` are session-gated
      and fully Swagger-annotated. Repo↔credential destinations from
      design §5 are deliberately descoped until mirror-sync decides.
      See [`docs/design/credential-vault.md`](docs/design/credential-vault.md).
- [x] **Phase F4 — Record query endpoint.** `GET /api/repos/{name}/records/{template}` lists all parsed records under `storage/<template>/*.meta.json` at HEAD, with optional `where` (equality/inequality on string fields, numeric range on scalars), `sort` (prefix `-` for descending), and `limit`. Filter DSL lives in `internal/formidable/query.go`; handler in `internal/server/handler_records.go`. Swagger, unit, handler, and Cucumber tests green. See [`structured-sync-api.md`](docs/design/structured-sync-api.md) §10.8 and §11 F4.
- [x] **Phase F3 — Binary transport for images.** Binary blobs under `storage/<template>/images/` flow through the existing `PUT /files/{path}` and `POST /commits` endpoints as ordinary base64-encoded content; the record-merge path (§10.3) explicitly skips images via `isFormidableRecordPath`. Same-path overwrite is accepted without conflict. Referential integrity is descoped — that's Formidable's concern, not GiGot's. Cucumber scenarios in `formidable_records.feature` cover round-trip and overwrite. See [`structured-sync-api.md`](docs/design/structured-sync-api.md) §10.5 and §11 F3.
- [x] **Phase F2 — Descoped.** Server-side schema validation would couple GiGot to Formidable's field-type model (rejected); template structural merge is handled well enough by the generic line-based merge. See [`structured-sync-api.md`](docs/design/structured-sync-api.md) §10.4, §10.7, and §11 F2 for rationale.
- [x] **Phase F1 — Structured per-field record merge.** `internal/formidable` implements the uniform merge rule from `structured-sync-api.md` §10.3: every `data.*` field in a `storage/**/*.meta.json` record resolves as one atomic value; same-field divergence is last-writer-wins by `meta.updated`; immutable meta keys (`created`, `id`, `template`) are the only conflict source. Wired into `PUT /files/{path}` and `POST /commits` for marker-stamped repos. Unit + handler + Cucumber tests green; new `formidable.RecordConflict` 409 shape documented in Swagger.
- [x] **Cucumber coverage for server-mode-driven behavior.** Integration feature `formidable_first.feature` plus the `the server is running in formidable-first mode` step exercise the §2.7 decision matrix (init/clone × default/override) end-to-end through the HTTP pipeline, including a wire-level idempotence proof against a pre-marked upstream.
- [x] **CLI redesign with grouped `-help`.** One `-init` flag plus a `-formidable-first` sub-flag replaces the earlier standalone `--init-formidable`; `gigot -help` prints grouped help. Parse/dispatch split (`internal/cli/cli.go`) makes every flag combination exhaustively unit-testable.
- [x] **Config-driven marker provisioning** (design doc §2.7): `server.formidable_first` flips the default so both init and clone stamp `.formidable/context.json`; per-request `scaffold_formidable: true`/`false` overrides either direction. Clone-stamp is idempotent when the upstream already carries a valid marker.
- [x] Leaf `internal/crypto` NaCl-box package + on-disk keypair bootstrap
- [x] Client enrollment endpoint
- [x] Sealed-body middleware for `/api/*`
- [x] Encrypted persistent token store
- [x] Admin page + password/session login
- [x] Per-repo access on subscription keys (enforced via `internal/policy`)
- [x] `./gigot -rotate-keys` with atomic rewrap + backups
- [x] Central `policy.Evaluator` + `DenyAll` / `AllowAuthenticated` /
      `TokenRepoPolicy`
- [x] Models split per concern (`models_*.go`)
- [x] Roles ripped out end-to-end
- [x] Sidebar-layout admin UI with deep-linkable panels
- [x] Optional Formidable-context scaffold on repo creation
- [x] Graceful SIGINT/SIGTERM shutdown via `http.Server.Shutdown`, with a
      stale-port startup error pointing at `lsof -iTCP:<port>`

---

## Quick Start

```bash
# 1. Build (use `make build` for a version-stamped binary; plain
#    `go build .` works too but produces 0.0.0-dev+<commit>).
make build

# 2. Generate a default config next to the binary
./gigot -init
# → Wrote default gigot.json

# 3. Create your first admin account
./gigot -add-admin alice
# Password for alice:
# Confirm password:
# → Admin "alice" saved

# 4. Run the server
./gigot
# GiGot server starting on 127.0.0.1:3417
# Repository root: ./repos
# Admin UI: http://127.0.0.1:3417/admin
```

Now point a browser at `http://127.0.0.1:3417/admin`, log in as `alice`, and
issue a subscription key. Hand that key to a Formidable client to grant access.

---

## Formidable-Context Scaffolding

When you create a new repo on the admin page, you can tick **Scaffold as
Formidable context**. The checkbox defaults **off** — a vanilla empty bare
repo is what you get without it. With the box ticked, the fresh repo is
seeded with one initial commit containing the directory layout
[Formidable](https://github.com/petervdpas/Formidable) expects:

```
README.md
templates/
  basic.yaml    # minimal starter: GUID + text with `collection: entries`
storage/
  .gitkeep      # empty placeholder so the dir is tracked
.formidable/
  context.json  # marker: { version, scaffolded_by, scaffolded_at }
```

The static files live as real files in the GiGot source tree under
`internal/server/scaffold/formidable/` and are embedded into the binary via
`//go:embed all:scaffold/formidable`. The marker file `.formidable/context.json`
is generated at scaffold time so its `scaffolded_at` timestamp is accurate;
a `formidable_first: true` server reads it to decide whether a given repo
gets schema-aware sync behaviour (see
[`docs/design/structured-sync-api.md`](docs/design/structured-sync-api.md) §2.5).
To change the static starter content, edit the embedded files and rebuild —
no Go string literals to maintain.

The scaffold commit is authored and committed by
`GiGot Scaffolder <scaffold@gigot.local>` (hardcoded). Every subsequent
commit comes from whichever Formidable client pushed it, carrying that
client's real git identity — GiGot does not rewrite pushed commits.

You can also trigger scaffolding from the API directly:

```bash
curl -X POST http://localhost:3417/api/repos \
  -H 'Content-Type: application/json' \
  -b /tmp/gigot-admin-cookie \
  -d '{"name":"my-templates","scaffold_formidable":true}'
```

Verify what landed in a new repo (bare, so you need ls-tree or a clone):

```bash
git -C repos/my-templates.git ls-tree -r HEAD --name-only
# README.md
# storage/.gitkeep
# templates/basic.yaml
```

---

## Command-Line Interface

The `gigot` binary has one daemon mode and four one-shot command
families. `-init`, `-add-admin`, `-rotate-keys`, and the
`-wipe-*` / `-factory-reset` destructive family are mutually exclusive
with each other; running `gigot` with none of them starts the HTTP
server. `gigot -help` prints the same grouped help shown below.

**Run mode (default when no one-shot flag is set):**

| Flag              | Description                                                                                       |
| ----------------- | ------------------------------------------------------------------------------------------------- |
| `-config <path>`  | Path to `gigot.json`. Defaults to `./gigot.json`. Missing file falls back to built-in defaults.   |

**One-shot commands (each exits after running; mutually exclusive):**

| Flag                       | Description                                                                                                                              |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `-init`                    | Writes a fresh `gigot.json` into the current directory and exits. Will not overwrite by accident — you own the file.                     |
| &nbsp;&nbsp;`-formidable-first` | Sub-flag of `-init`: pre-enables `server.formidable_first` in the emitted config, so both init and clone stamp the Formidable context marker by default (design doc §2.5/§2.7). Rejected when used without `-init`. |
| `-add-admin <username>`    | Creates (or overwrites) an admin account with the given username and exits. Prompts for a password on stdin.                             |
| `-rotate-keys`             | Generates a fresh server keypair, re-encrypts all sealed stores under it, backs up the previous files as `.bak.{timestamp}`, and exits. **Stop the server first.** |
| `-healthcheck`             | Probes `http://<server.host>:<server.port>/` with a 2-second timeout; exits 0 on a 2xx response and 1 otherwise. Wired from the Dockerfile `HEALTHCHECK` because the distroless runtime image has no `curl` / `wget`. A `0.0.0.0` / `::` bind in the config is rewritten to `127.0.0.1` so the probe is always loopback-local. |
| `-version`                 | Prints the binary version (`gigot <version>`) and exits. The version string is baked in at build time via `-ldflags "-X main.appVersion=<v>"` — both `release.yml` and the `Dockerfile` set it to the git tag (with the leading `v` stripped). A plain `go build .` with no ldflag falls back to `0.0.0-dev+<commit>` (and `+<commit>.dirty` for an uncommitted working tree), pulled from `runtime/debug.BuildInfo` — so a developer rebuild self-describes without needing to remember the ldflag invocation. |

**Destructive one-shots (compose with each other; mutually exclusive with `-init` / `-add-admin` / `-rotate-keys`):**

All destructive flags prompt for the literal word `yes` before acting.
Pass `-yes` to bypass the prompt in scripted contexts. Every wipe is
idempotent — removing a target that is already absent is not an error.

| Flag                   | Description                                                                                                                    |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `-wipe-repos`          | Remove every bare repository under `storage.repo_root`, including their audit chains. **Stop the server first.**               |
| `-wipe-admins`         | Remove `data/accounts.enc` (and the legacy `data/admins.enc` migration backup). All admin and regular accounts gone; recreate with `-add-admin`. |
| `-wipe-tokens`         | Remove `data/tokens.enc`. All subscription keys revoked.                                                                       |
| `-wipe-clients`        | Remove `data/clients.enc`. All enrolled client pubkeys dropped; clients will need to re-enroll.                                |
| `-wipe-sessions`       | Remove `data/sessions.enc`. All active admin sessions dropped; operators must log in again.                                    |
| `-wipe-credentials`    | Remove `data/credentials.enc`. Outbound credential vault emptied. Mirror destinations referencing these credentials will dangle until you also wipe or re-create them. |
| `-wipe-destinations`   | Remove `data/destinations.enc`. All per-repo mirror-sync destinations dropped.                                                 |
| `-factory-reset`       | Superset of every wipe above, **plus** the keypair (`data/server.key` / `data/server.pub`) and all rotation backups (`data/*.bak.*`). Restores a clean-install state, preserving only `gigot.json`. Rejected when combined with any granular `-wipe-*` flag. **Stop the server first.** |
| `-yes`                 | Skip the confirmation prompt. Valid only with a `-wipe-*` or `-factory-reset` flag.                                            |

**Demo setup (for the shipped Postman collection):**

| Flag                   | Description                                                                                                                                                                                 |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-add-demo-setup`      | Provisions admin `demo` / password `demo-password`, scaffolded repo `postman-demo`, credential `postman-pat`, and prints a fresh subscription token. Re-runnable; tokens stack. **Stop the server first.** |
| `-remove-demo-setup`   | Reverses `-add-demo-setup`. Revokes every token ever issued to the demo user, removes the credential, the repo, and the admin. Idempotent on a clean data dir.                              |

**Help:**

| Flag          | Description                 |
| ------------- | --------------------------- |
| `-help`, `-h` | Show the grouped help.      |

### Examples

```bash
# Generate a default config
./gigot -init

# Generate a config pre-configured for Formidable-first mode
./gigot -init -formidable-first

# Run with a non-default config path
./gigot -config /etc/gigot/gigot.json

# Rotate the server keypair (e.g. after a suspected leak, or before making
# the repo public). Stop the server first.
./gigot -rotate-keys

# After you've confirmed the rotated server works (admin login succeeds,
# clients reconnect), purge the rollback backups. The old server.key.bak.*
# is the leaked key you just rotated away from; do not leave it on disk.
rm data/*.bak.*

# Add an admin non-interactively (e.g. from a deploy script)
printf 'hunter2\nhunter2\n' | ./gigot -add-admin ci-admin

# Wipe just the subscription keys after a suspected leak, keeping admin
# accounts, repos, and the keypair intact. Stops to confirm unless -yes.
./gigot -wipe-tokens

# Compose several granular wipes into one invocation.
./gigot -wipe-tokens -wipe-sessions -yes

# Nuke everything back to a clean-install state. Only gigot.json survives.
./gigot -factory-reset
```

> The password prompt uses `golang.org/x/term` when stdin is a TTY (so nothing is
> echoed). When stdin is piped (CI, scripts), GiGot falls back to line-based
> reads, which is why you pipe the password twice — once for the prompt, once
> for the confirmation.

---

## Configuration Reference (`gigot.json`)

A full `gigot.json` looks like this:

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 3417,
    "formidable_first": false
  },
  "storage": {
    "repo_root": "./repos"
  },
  "auth": {
    "enabled": false,
    "type": "token"
  },
  "crypto": {
    "private_key_path": "./data/server.key",
    "public_key_path":  "./data/server.pub",
    "data_dir":         "./data"
  },
  "logging": {
    "level": "info"
  }
}
```

All relative paths are resolved relative to the directory that contains the
config file, not the process's working directory. This makes it safe to invoke
`gigot -config /etc/gigot/gigot.json` from anywhere.

### `server`

| Field            | Type   | Default       | Description                                                                                                                      |
| ---------------- | ------ | ------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| host             | string | `"127.0.0.1"` | Bind address. Set to `0.0.0.0` to accept external traffic.                                                                        |
| port             | int    | `3417`        | TCP port.                                                                                                                         |
| formidable_first | bool   | `false`       | Deployment-level Formidable-mode switch (design doc §2.5/§2.7). When `true`, `POST /api/repos` stamps `.formidable/context.json` on both init and clone by default (idempotent on clones that already carry a valid marker). Per-request `scaffold_formidable: true`/`false` overrides this — `false` is the escape hatch for hosting a plain repo or mirroring a plain upstream. |

### `storage`

| Field     | Type   | Default     | Description                                                                  |
| --------- | ------ | ----------- | ---------------------------------------------------------------------------- |
| repo_root | string | `"./repos"` | Directory containing bare Git repositories. Created on demand per repo name. |

### `auth`

| Field   | Type   | Default   | Description                                                                                                                    |
| ------- | ------ | --------- | ------------------------------------------------------------------------------------------------------------------------------ |
| enabled | bool   | `false`   | Master switch for bearer-token authentication on the `/api/*` and `/git/*` paths. When `false`, all non-admin endpoints are open. |
| type    | string | `"token"` | Reserved for future strategy selectors. Currently only `token` is meaningful; session auth for the admin UI is always on.      |

### `crypto`

| Field             | Type   | Default              | Description                                                                                                                                 |
| ----------------- | ------ | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| private_key_path  | string | `"./data/server.key"` | The server's curve25519 private key, base64-encoded in a 0600 file. Generated automatically on first run if missing.                       |
| public_key_path   | string | `"./data/server.pub"` | Matching public key in a 0644 file. Also generated on first run.                                                                           |
| data_dir          | string | `"./data"`            | Where encrypted stores live: `clients.enc` (enrolled Formidable clients), `tokens.enc` (subscription keys), `accounts.enc` (admin + regular accounts; `admins.enc` left alongside it is a one-release migration backup), `credentials.enc` (outbound credential vault), `destinations.enc` (per-repo mirror destinations), `sessions.enc` (active admin sessions). |

### `logging`

| Field | Type   | Default  | Description                                                 |
| ----- | ------ | -------- | ----------------------------------------------------------- |
| level | string | `"info"` | Log level (reserved — current code uses standard `log.*`). |

### Partial configs

The loader merges your config into the built-in defaults, so you can keep your
`gigot.json` minimal. For example, to just override the port and repo root:

```json
{
  "server":  { "port": 4000 },
  "storage": { "repo_root": "/var/lib/gigot/repos" }
}
```

---

## On-Disk Data Layout

After `-init` and a first run, you'll see something like:

```
./
├── gigot                     # the binary
├── gigot.json                # config
├── repos/                    # bare git repositories (storage.repo_root)
│   └── my-templates.git/
└── data/                     # (crypto.data_dir)
    ├── server.key            # 0600 — NaCl private key (base64)
    ├── server.pub            # 0644 — NaCl public key  (base64)
    ├── clients.enc           # sealed: enrolled clients + their pubkeys
    ├── tokens.enc            # sealed: issued subscription keys
    ├── accounts.enc          # sealed: admin + regular accounts (provider, identifier, role, bcrypt hash for local)
    ├── admins.enc            # sealed: legacy admin store (migration backup, left in place for one release)
    ├── credentials.enc       # sealed: outbound credentials (PATs, SSH keys, …)
    ├── destinations.enc      # sealed: per-repo mirror-sync destinations
    └── sessions.enc          # sealed: active admin sessions (restart-survives)
```

The sealed `.enc` files are NaCl-sealed to the server's own public key. **Only a
GiGot process holding the matching `server.key` can read them.** If you lose
`server.key`, you lose every admin account, every subscription key, every
enrolled client pubkey, every stored outbound credential, every configured
mirror destination, and every active admin session — there is no recovery.

**Back up `server.key` somewhere safe.**

---

## Authentication Overview

GiGot ships three authentication strategies, all pluggable via a shared
`auth.Provider`:

| Strategy   | Where it applies                                | Credential                                        |
| ---------- | ----------------------------------------------- | ------------------------------------------------- |
| `token`    | `/api/*` (except bootstrap paths) and `/git/*` | `Authorization: Bearer <token>` header            |
| `session`  | `/api/admin/*` and the `/admin` UI              | `gigot_session` cookie set by `/admin/login`      |
| *(gateway)* | Future (Task 7): trust a signed header from a fronting gateway | To be configured                  |

### When is auth enforced?

- With `auth.enabled = false` (the default), only admin endpoints are gated.
  The rest of the API is open — convenient for development, dangerous in
  production.
- With `auth.enabled = true`, any request that is not on the public-paths list
  must present a valid bearer token or session cookie.

### Public paths (never require auth)

- `GET /` — status page
- `GET /api/crypto/pubkey` — bootstrap: clients fetch the server pubkey here
- `POST /api/clients/enroll` — bootstrap: clients register their pubkey here
- `GET /api/admin/session` — returns 401 internally when not logged in (the UI
  uses this to decide whether to show the login form)
- `/admin`, `/admin/`, `/admin/login`, `/admin/logout` — admin page endpoints
- `/swagger/*` — Swagger UI

Everything else (`/api/health`, `/api/repos*`, `/api/auth/token`, `/git/*`,
`/api/admin/tokens`) requires authentication when `auth.enabled = true`.

---

## End-to-End Encrypted Bodies

GiGot can transparently **seal** request/response bodies between enrolled
Formidable clients and the server, independent of TLS. This is the main reason
the project exists in its current form: even an API gateway that terminates
TLS between the client and GiGot cannot read the payload.

### Wire format

A sealed request carries two plain HTTP headers the gateway can still see:

- `Content-Type: application/vnd.gigot.sealed+b64`
- `X-Client-Id: <enrolled client id>`

The request body is:

```
base64( nonce[24] || box.Seal(jsonBytes, recipientPub=serverPub, senderPriv=clientPriv) )
```

The response is symmetric: the server's middleware seals the handler's output
with `box.Seal(bodyBytes, recipientPub=clientPub, senderPriv=serverPriv)`,
writes `Content-Type: application/vnd.gigot.sealed+b64`, and streams the
base64-encoded result.

### Opt-in, not mandatory

The sealed middleware is **transparent**: a request that lacks both the
content-type marker and the `X-Client-Id` header is passed through as normal
JSON. This lets plain `curl` clients, Swagger UI, and the admin browser work
without understanding NaCl. Only Formidable clients that want end-to-end
encryption need to adopt the sealed format.

### Skipped paths

The sealed middleware only acts on `/api/*`. The index page, Swagger UI, and
`/git/*` (which speaks Git's own protocol) are never sealed.

### Example (pseudocode)

```ts
// 1) Fetch server pubkey (unauth, plain):
const serverPub = (await fetch('/api/crypto/pubkey').then(r => r.json())).public_key;

// 2) Enroll once:
await fetch('/api/clients/enroll', {
  method: 'POST',
  body: JSON.stringify({ client_id: 'laptop-01', public_key: myPubB64 }),
});

// 3) From now on, seal every API request:
const sealed = nacl.box(utf8(JSON.stringify(body)), nonce, serverPubRaw, myPrivRaw);
await fetch('/api/repos', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/vnd.gigot.sealed+b64',
    'X-Client-Id':  'laptop-01',
    'Authorization': 'Bearer ' + subscriptionKey,
  },
  body: base64(concat(nonce, sealed)),
});
```

---

## Client Enrollment Flow

A Formidable client becomes known to the server in two steps:

1. **Fetch the server's public key** (unauthenticated):
   ```
   GET /api/crypto/pubkey
   → { "public_key": "<base64>" }
   ```
2. **Enroll the client's public key** (unauthenticated, once):
   ```
   POST /api/clients/enroll
   { "client_id": "laptop-01", "public_key": "<base64>" }
   → 201 { "client_id": "laptop-01", "server_public_key": "<base64>" }
   ```

Re-enrolling the same `client_id` with the **same** public key is idempotent.
Re-enrolling with a **different** public key returns `409 Conflict` — delete
the client first (future admin feature) or pick a new ID.

Enrollment does *not* grant access: it only lets the server seal responses to
that client. The admin must still issue a subscription key for the client to
hit the data endpoints.

> In a production deployment you typically put the gateway in front of
> `/api/clients/enroll` with some out-of-band gating (an enrollment password,
> a pre-shared key, Azure APIM subscription approval, etc.) so random strangers
> can't register themselves.

---

## Admin UI and Admin API

The admin UI lives at **`/admin`** and is a single self-contained HTML+JS page
for repositories and subscription keys. A sibling page at **`/admin/credentials`**
manages the outbound credential vault (PATs, SSH keys, etc.) that GiGot uses
when it talks to third-party systems on your behalf — see
[`docs/design/credential-vault.md`](docs/design/credential-vault.md).

### Bootstrap

Because the admin UI needs an account to log into, you create the first admin
with the CLI:

```bash
./gigot -add-admin alice
```

The account is stored in `data/accounts.enc` (sealed), so it survives restarts.

### Session model

- `POST /admin/login` with JSON `{ "username", "password" }` returns 200 + a
  `gigot_session` HTTP-only cookie valid for 12 hours.
- `POST /admin/logout` clears the session.
- All `/api/admin/*` endpoints require the session cookie.

> `Secure` is intentionally **not** set on the session cookie by default. TLS
> is typically terminated at a fronting gateway in our deployment targets. If
> you expose GiGot directly over HTTPS you should plumb a `Secure: true` flag
> via config (not yet implemented — follow-up work).

### Admin API

| Method | Path                    | Purpose                                                                               |
| ------ | ----------------------- | ------------------------------------------------------------------------------------- |
| GET    | `/api/admin/session`              | Returns the current admin identity or 401. The page polls this on load.                |
| GET    | `/api/admin/tokens`               | Lists every issued subscription key.                                                   |
| POST   | `/api/admin/tokens`               | Issues a new subscription key. Body: `{ "username", "repo", "abilities?": [...] }`. One key per `(username, repo)` — duplicate returns 409. |
| PATCH  | `/api/admin/tokens`               | Changes the repo binding or abilities on an existing key. Body: `{ "token", "repo?", "abilities?": [...] }`. |
| DELETE | `/api/admin/tokens`               | Revokes a subscription key. Body: `{ "token": "<value>" }`.                             |
| GET    | `/api/admin/credentials`          | Lists credential metadata (secret is never returned).                                   |
| POST   | `/api/admin/credentials`          | Creates a credential. Body: `{ "name", "kind", "secret", "expires?", "notes?" }`.      |
| GET    | `/api/admin/credentials/{name}`   | Metadata for one credential.                                                            |
| PATCH  | `/api/admin/credentials/{name}`   | Rotate/update metadata. Any omitted field is left unchanged.                            |
| DELETE | `/api/admin/credentials/{name}`   | Remove a credential. **409** with `{ ref_repos: [...] }` when any repo destination still points at it. |
| GET    | `/api/admin/repos/{name}/destinations`        | Lists mirror-sync destinations attached to a repo.                          |
| POST   | `/api/admin/repos/{name}/destinations`        | Adds a destination. Body: `{ "url", "credential_name", "enabled?" }`. **404** if `credential_name` is not in the vault. |
| GET    | `/api/admin/repos/{name}/destinations/{id}`   | Metadata for one destination.                                               |
| PATCH  | `/api/admin/repos/{name}/destinations/{id}`   | Update any of `url` / `credential_name` / `enabled`; omitted fields unchanged. |
| DELETE | `/api/admin/repos/{name}/destinations/{id}`   | Remove a destination.                                                        |
| POST   | `/api/admin/repos/{name}/destinations/{id}/sync` | Fire a one-shot mirror push for this destination. Synchronous. Returns the updated destination with `last_sync_status`, `last_sync_at`, and (on failure) `last_sync_error` populated. |

The legacy unauthenticated `POST /api/auth/token` still exists for backward
compatibility, but the admin UI uses `/api/admin/tokens` (session-gated) for
everything.

---

## HTTP API Reference

### Health & index

| Method | Path           | Auth | Description                                           |
| ------ | -------------- | ---- | ----------------------------------------------------- |
| GET    | `/`            | —    | HTML status page                                      |
| GET    | `/api/health`  | bearer (if enabled) | JSON `{ "status": "ok" }`                |

### Crypto & enrollment

| Method | Path                   | Auth | Description                                 |
| ------ | ---------------------- | ---- | ------------------------------------------- |
| GET    | `/api/crypto/pubkey`   | —    | Returns the server's NaCl public key.      |
| POST   | `/api/clients/enroll`  | —    | Registers a client public key. Idempotent for same key, 409 for conflicting key. |

### Repositories

| Method | Path                               | Auth   | Description                           |
| ------ | ---------------------------------- | ------ | ------------------------------------- |
| GET    | `/api/repos`                       | bearer | List all repositories                 |
| POST   | `/api/repos`                       | bearer | Create a repository (body: `{ "name" }`) |
| GET    | `/api/repos/{name}`                | bearer | Repo details                          |
| DELETE | `/api/repos/{name}`                | bearer | Delete a repo                         |
| GET    | `/api/repos/{name}/status`         | bearer | Working status                        |
| GET    | `/api/repos/{name}/branches`       | bearer | List branches                         |
| GET    | `/api/repos/{name}/log`            | bearer | Commit log                            |
| GET    | `/api/repos/{name}/head`           | bearer | Current HEAD SHA + default branch     |
| GET    | `/api/repos/{name}/tree`           | bearer | Recursive blob listing at a version   |
| GET    | `/api/repos/{name}/snapshot`       | bearer | All blobs at a version (base64)       |
| GET    | `/api/repos/{name}/files/{path}`   | bearer | One blob at a version (base64)        |
| PUT    | `/api/repos/{name}/files/{path}`   | bearer | Write one file with fast-forward/auto-merge/409-conflict semantics |
| POST   | `/api/repos/{name}/commits`        | bearer | Atomic multi-file commit (put/delete ops); transactional 409 on any conflict |
| GET    | `/api/repos/{name}/changes`        | bearer | Paths added/modified/deleted between a client's `since` version and current HEAD |

### Tokens (legacy)

| Method | Path                | Auth   | Description               |
| ------ | ------------------- | ------ | ------------------------- |
| POST   | `/api/auth/token`   | bearer | Issue a token (legacy).   |
| DELETE | `/api/auth/token`   | bearer | Revoke a token (legacy).  |

### Admin

See [Admin UI and Admin API](#admin-ui-and-admin-api) above.

### Swagger

A full, machine-generated OpenAPI spec lives at `/swagger/index.html`. The raw
JSON and YAML are in `docs/`.

---

## Git Smart-HTTP Endpoints

GiGot speaks Git's smart-HTTP protocol so a repo can be cloned and pushed to
like any other remote. These endpoints sit under `/git/{name}/...`:

| Method  | Path                                 | Description                      |
| ------- | ------------------------------------ | -------------------------------- |
| GET     | `/git/{name}/info/refs`              | Ref advertisement.               |
| POST    | `/git/{name}/git-upload-pack`        | Fetches (`git clone`, `git fetch`). |
| POST    | `/git/{name}/git-receive-pack`       | Pushes (`git push`).              |

Example:

```bash
git clone http://alice:<subscription-key>@gigot.example.com/git/my-templates
```

Git endpoints **do not** participate in the sealed-body layer — Git has its own
wire protocol that can't be wrapped. Rely on TLS between the client and the
server (or gateway) for confidentiality here.

---

## Deployment Modes

### 1. Standalone

Run the binary directly on a host, optionally behind nginx/caddy for TLS:

```bash
./gigot -config /etc/gigot/gigot.json
```

Point `gigot.json` at a persistent data directory:

```json
{
  "server":  { "host": "0.0.0.0", "port": 3417 },
  "storage": { "repo_root": "/var/lib/gigot/repos" },
  "auth":    { "enabled": true, "type": "token" },
  "crypto":  {
    "private_key_path": "/var/lib/gigot/data/server.key",
    "public_key_path":  "/var/lib/gigot/data/server.pub",
    "data_dir":         "/var/lib/gigot/data"
  }
}
```

Suggested systemd unit:

```ini
[Unit]
Description=GiGot — git-backed server for Formidable
After=network-online.target

[Service]
Type=simple
User=gigot
WorkingDirectory=/var/lib/gigot
ExecStart=/usr/local/bin/gigot -config /etc/gigot/gigot.json
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### 2. Container image (Docker / Compose)

Tagged releases publish a multi-arch image
(`linux/amd64` + `linux/arm64`) to Docker Hub automatically. Operators
have three pull options:

| Tag                          | Use it for                                              |
| ---------------------------- | ------------------------------------------------------- |
| `petervdpas/gigot:latest`    | Always-newest pull. Good for quick demos and homelabs.  |
| `petervdpas/gigot:X.Y.Z`     | Pinned production deploys (e.g. `petervdpas/gigot:0.4.0`). Survives a future `latest` regression. |
| `petervdpas/gigot:dev`       | Hand-pushed development tag (no provenance/SBOM). Drifts ahead of releases between tags. |

For source builds — useful while iterating on the Dockerfile itself:

```bash
docker build -t gigot:dev --build-arg VERSION=dev .
```

The image is multi-stage (`golang:1.25-alpine` → `gcr.io/distroless/static-debian12:nonroot`)
and runs as uid `65532`. The binary is the entrypoint; everything else —
config, keys, tokens, repos — lives on mounted volumes:

| Mount                          | Contents                  | Persistence |
| ------------------------------ | ------------------------- | ----------- |
| `/var/lib/gigot/data`          | keypair, sealed stores    | **Required.** Lose this and every encrypted store is unrecoverable. |
| `/var/lib/gigot/repos`         | bare repos + audit chains | Required. |
| `/etc/gigot/gigot.json`        | config (read-only mount)  | Required. No default is baked in — the server fails fast if missing. |

Two image-specific defaults differ from the standalone `gigot.json` example
above and matter for any container deploy:

- `server.host` is `0.0.0.0`. `127.0.0.1` inside a container means
  "unreachable from the host."
- `auth.enabled` is `true`. Open `/api/*` is fine for a laptop dev loop, not
  for a long-lived deploy.

A minimal container-side `gigot.json`:

```json
{
  "server":  { "host": "0.0.0.0", "port": 3417 },
  "storage": { "repo_root": "/var/lib/gigot/repos" },
  "auth":    { "enabled": true,  "type": "token" },
  "crypto":  {
    "private_key_path": "/var/lib/gigot/data/server.key",
    "public_key_path":  "/var/lib/gigot/data/server.pub",
    "data_dir":         "/var/lib/gigot/data"
  }
}
```

**First-run prep — host directory ownership.** Distroless-nonroot is
uid 65532, but host directories created via `mkdir` or `docker volume create`
land as root, so the process can't write to them. Pre-chown the bind targets
once before any container starts:

```bash
mkdir -p /srv/gigot/data /srv/gigot/repos
sudo chown -R 65532:65532 /srv/gigot/data /srv/gigot/repos
```

**Bootstrap an admin account.** `-add-admin` reads a password from stdin, so
run it once as a transient interactive container against the same volumes the
server will use:

```bash
docker run --rm -it \
  -v /srv/gigot/data:/var/lib/gigot/data \
  -v /srv/gigot/repos:/var/lib/gigot/repos \
  -v $(pwd)/gigot.json:/etc/gigot/gigot.json:ro \
  petervdpas/gigot:latest \
  -add-admin alice
```

`-rotate-keys`, `-wipe-clients`, and the other one-shot flags follow the same
pattern: same volumes, the flag as argv. There's no `GIGOT_ADMIN_PASSWORD`
env var by design — passwords would otherwise leak into `docker inspect` and
process listings (see design doc §6.2).

**Run the server (raw Docker):**

```bash
docker run -d --name gigot \
  -p 3417:3417 \
  -v /srv/gigot/data:/var/lib/gigot/data \
  -v /srv/gigot/repos:/var/lib/gigot/repos \
  -v $(pwd)/gigot.json:/etc/gigot/gigot.json:ro \
  petervdpas/gigot:latest
```

**Run the server (Compose, recommended).** A `docker-compose.yml` lives at
the repo root. From a directory containing your `gigot.json` and a
pre-chowned `data/` and `repos/` (same shape as above):

```bash
docker compose -f /path/to/GiGot/docker-compose.yml up -d
```

Compose defaults to `petervdpas/gigot:dev` (the rolling development tag)
but also keeps a `build:` stanza pointing at the repo, so cloning the
source and running `docker compose up --build` works without an internet
pull. For pinned production deploys, edit `image:` to a specific
release like `petervdpas/gigot:0.4.0`.

**Healthcheck.** The image declares a `HEALTHCHECK` directive that runs the
binary's own `-healthcheck` flag every 30s — distroless has no `curl` or
`wget`, so the binary probes itself. `docker ps` will show `(healthy)` once
the listener is up; `docker inspect <container>` exposes the per-probe log
under `.State.Health` for orchestrators that key off it. The probe hits
`http://127.0.0.1:<server.port>/`, so a `0.0.0.0` bind is correctly mapped
to loopback on the way in.

**Tagged-release publishing.** The `publish-image` job in
`.github/workflows/release.yml` fires on every `v*` tag push. It builds
multi-arch (`linux/amd64` + `linux/arm64`), pushes
`petervdpas/gigot:<version>` and `:latest` to Docker Hub, and bakes
provenance + SBOM attestations into the manifest so Docker Scout's
supply-chain check stays green. Two repo secrets must exist for the job
to run: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` (a Docker Hub PAT
with Read & Write scope).

### 3. Behind Azure API Management (or similar)

The gateway takes over TLS termination, caller identity (OAuth, subscription
keys, etc.), rate limiting, and coarse routing. GiGot's own auth can usually
stay **off** for `/api/*` in this mode, because the gateway is doing that
job — but note that means the gateway is trusted.

Recommended APIM configuration:

- Require an APIM subscription key on every route GiGot exposes.
- Apply rate-limit and quota policies in APIM, not in GiGot.
- **Don't** try to add WAF rules on request bodies for `/api/*` — bodies are
  NaCl-sealed and look like opaque base64 to the gateway. You can still inspect
  headers and URLs.
- For `/git/*`, let APIM pass through as-is; Git's protocol is binary and
  multiplexed.
- Forward `Authorization`, `X-Client-Id`, and `Content-Type` headers verbatim
  so the sealed-body middleware and bearer-auth strategy still work.

The planned gateway-trust strategy (Task 7) will let GiGot accept a signed
identity header from APIM so admin-UI actions can be performed without a second
login when you're already authenticated at the gateway.

---

## Security Model and Tradeoffs

- **Server keypair is a single point of failure.** Losing `server.key` invalidates
  every encrypted store. Back it up, and preferably keep a copy offline.
- **Rotation is a one-liner.** If you suspect a leak (or are about to flip a
  previously-private repo public), stop the server and run
  `./gigot -rotate-keys`. It generates a fresh keypair, decrypts every sealed
  store with the old key in memory, re-encrypts under the new one, and backs up
  the previous files as `.bak.{timestamp}` so you can inspect or roll back.
  Admin accounts, subscription tokens, and enrolled client pubkeys all survive.
  Formidable clients pick up the new server pubkey on their next
  `/api/crypto/pubkey` fetch and keep working.
- **Delete rotation backups once you're satisfied.** After `-rotate-keys`,
  `data/server.key.bak.{timestamp}` still contains the *old* private key —
  which is exactly the key material you rotated away from. Keeping it defeats
  the rotation. Once the server comes back up, an admin can log in, and any
  Formidable clients reconnect, run `rm data/*.bak.*` to purge the backups.
  They are rollback-only insurance; they are not ongoing history.
- **Persistent admin sessions.** Admin sessions are stored in the sealed
  `data/sessions.enc` file so they survive a restart — no re-login after
  `./gigot -rotate-keys`, a routine deploy, or an ordinary bounce. The
  tradeoff is that an attacker holding `server.key` can now read active
  session IDs in addition to admins/tokens/credentials/destinations;
  in practice the blast radius of a compromised server key is already
  "full server takeover," so this moves the needle marginally. Expired
  entries are scrubbed from the file on load so the file never grows
  unbounded. A true multi-instance-HA setup (two GiGot processes behind a
  load balancer sharing state) still needs a shared store like Redis —
  the sealed-file approach only handles *restart-survives*, not
  *concurrent-writers*.
- **Bearer tokens are opaque, not JWTs.** GiGot issues random 32-byte tokens
  that are looked up server-side. They can be revoked. They do not carry claims.
  Each token is bound to an **allowlist of repositories** the bearer may
  access (see below); management actions (creating repos, issuing keys,
  managing admins) are reserved for admin sessions.
- **Per-repo scoping is enforced centrally.** `internal/policy.TokenRepoPolicy`
  gates every `/api/repos/*` and `/git/*` route. A subscription key is
  bound to exactly one repo — set at issue time (`POST /api/admin/tokens`)
  and rebound later (`PATCH /api/admin/tokens`). Listing (`GET /api/repos`)
  returns only that bound repo to token callers; admins see everything.
  A user who needs access to several repos receives several keys
  (one per repo); uniqueness on `(account, repo)` is enforced.
- **bcrypt cost.** `bcrypt.DefaultCost` (10) is used for admin passwords. Adjust
  in `internal/admins/store.go` if your hardware warrants it.
- **NaCl box, not OpenPGP.** Despite occasional shorthand, the crypto used is
  not PGP. It's NaCl's authenticated public-key encryption (curve25519 +
  XSalsa20 + Poly1305), chosen for its small, misuse-resistant API.
- **Sealed bodies block gateway content inspection.** This is the whole point.
  If you need a gateway WAF to inspect payloads, do it before sealing (i.e. in
  the client) or don't seal those routes.
- **`auth.enabled = false` is for dev only.** With auth off, anyone who can
  reach the port can list/create/delete repos.

---

## Development and Testing

### Run all tests

```bash
go test ./...
```

### Run only the integration (Cucumber/godog) suite

```bash
go test ./integration/...
```

All scenarios live under `integration/features/*.feature`. Step definitions are
in `integration/integration_test.go`. When you add a handler, consider adding a
feature file alongside a unit test so behaviour is covered at both levels.

### Regenerate the Swagger spec

```bash
swag init -g main.go -o docs
```

### Useful test paths

- `internal/crypto/*_test.go` — NaCl box roundtrips, tamper detection, on-disk keypair, `-rotate-keys` rewrap flow.
- `internal/clients/*_test.go` — enrollment store, idempotent re-enrollment.
- `internal/auth/*_test.go` — token strategy, session strategy, sealed token persister.
- `internal/admins/*_test.go` — admin store + bcrypt verify.
- `internal/credentials/*_test.go` — credential vault store (create / rotate / delete / persist / touch).
- `internal/destinations/*_test.go` — per-repo mirror destinations store (CRUD + `Refs` + cascade cleanup).
- `internal/formidable/*_test.go` — record-merge rules from structured-sync-api.md §10.
- `internal/git/audit_test.go` — audit-chain append, CAS retry, JSON roundtrip on `refs/audit/main`.
- `internal/policy/*_test.go` — `TokenRepoPolicy` per-repo scope decisions.
- `internal/server/*_test.go` — HTTP handlers, index page, repo router, admin endpoints.
- `integration/features/*.feature` — end-to-end Cucumber scenarios for every route.

---

## Project Structure

```text
GiGot/
├── main.go                           # Entry point — just calls cli.Execute
├── gigot.json                        # Server config (generated with -init)
├── docs/                             # Generated Swagger assets + design docs
│   ├── swagger.json / swagger.yaml   # Machine-generated OpenAPI
│   └── design/                       # Narrative design docs (hand-written)
├── integration/                      # Cucumber feature tests
│   ├── integration_test.go
│   └── features/*.feature
└── internal/
    ├── admins/                       # Admin account store (bcrypt + sealed file)
    ├── auth/                         # Provider, TokenStrategy, SessionStrategy, SealedTokenStore
    ├── cli/                          # CLI bootstrap: Parse → dispatch → Execute
    │   ├── cli.go                    # Flag definitions, Parse(), helpText()
    │   └── root.go                   # Execute() dispatch + runAddAdmin/runRotateKeys
    ├── clients/                      # Enrolled client pubkeys (sealed file)
    ├── config/                       # JSON config loading + defaults
    ├── credentials/                  # Outbound credential vault (sealed file)
    ├── crypto/                       # NaCl box wrappers + keypair bootstrap (leaf package)
    ├── destinations/                 # Per-repo mirror-sync destinations (sealed file)
    ├── formidable/                   # Record merge rules (structured-sync-api.md §10)
    ├── git/                          # Bare repo management + sync primitives
    ├── policy/                       # TokenRepoPolicy: per-repo scope decisions
    └── server/                       # HTTP server, routes, middleware, admin UI
        ├── server.go                 # Wiring
        ├── router.go                 # Sub-routers for /api/repos and /git
        ├── respond.go                # JSON + error helpers
        ├── middleware_sealed.go      # Sealed-body request/response middleware
        ├── handler_admin.go          # Admin login/logout + tokens
        ├── handler_admin_page.go     # /admin + /admin/credentials pages
        ├── handler_admin_credentials.go    # Credential vault REST
        ├── handler_admin_destinations.go   # Per-repo destinations REST
        ├── handler_clients.go        # Client enrollment
        ├── handler_crypto.go         # Server pubkey
        ├── handler_auth.go           # Legacy token endpoints
        ├── handler_repos.go          # Repository CRUD (with destinations cascade)
        ├── handler_health.go         # /api/health
        ├── handler_git.go            # Git smart-HTTP proxy
        ├── handler_sync.go           # Structured sync — /head /tree /snapshot /files /commits /changes
        ├── handler_records.go        # Formidable-first record query endpoint
        ├── formidable_merge.go       # Record-merge pipeline wired into PUT/commits
        ├── formidable_scaffold.go    # Formidable-context scaffold payload
        ├── repo_scope.go             # Token → allowlist filter used by handlers
        ├── templates.go / assets.go  # Embedded HTML + CSS/JS for /admin
        └── models*.go                # Request/response DTOs, split per concern
```

Every package aims to keep one clear responsibility. `internal/crypto` is a leaf
package with no imports from other internal packages so it can be reused (and
tested) without dragging the rest of the server in.
