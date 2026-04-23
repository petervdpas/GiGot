# Docker image for GiGot — should it exist, and what shape?

Status: **design-only, nothing shipped.** No `Dockerfile`, no
`docker-compose.yml`, no published image. This doc is the first pass
at whether containerized GiGot is worth doing and what the image
should look like if we do.

---

## 1. The actual question

The README documents exactly one install path: `go build -o gigot .`,
drop the binary on a host, run it under systemd (see README §11.1
"Standalone"). The `release.yml` workflow ships prebuilt tarballs
for `linux/amd64`, `linux/arm64`, and `windows/amd64` on tag push —
still native binaries, not images.

That is fine for the "one box, one sysadmin" case. It starts to hurt
the moment someone wants to:

- **Try GiGot without installing a Go toolchain** (`docker run gigot:latest`
  instead of a checkout + build).
- **Run it on a NAS / Synology / Unraid / home server** where the
  natural unit of deployment is a container, not a systemd unit.
- **Run it on Kubernetes** (a team running Formidable at scale may
  already have a k8s cluster and no place for a one-off binary).
- **Pin a version.** `ghcr.io/petervdpas/gigot:v0.3.1` is a single
  immutable artifact; a tarball + config + data dir requires the
  operator to reassemble state correctly after an upgrade.

Neither of those paths is *blocked* today — you can write your own
Dockerfile in ten lines. The question is whether GiGot should ship
one as a first-class artifact and commit to keeping it working.

---

## 2. Why containerize (and why not)

### 2.1 Case for

- **Distribution parity with the binary.** A tagged release already
  produces tarballs via `release.yml`. Adding an image to the same
  workflow is a small incremental cost and meaningfully lowers the
  "try it" barrier.
- **Immutable surface.** The image bundles the exact `gigot` binary,
  the embedded scaffold templates, and the known-good Go build flags.
  Operators stop having to match a tarball to a host libc or a Go
  version.
- **Home-lab / self-host audience.** GiGot is a 15-person team tool
  (see accounts design doc); many of those teams run Portainer,
  Unraid, or a docker-compose file on a homelab box, not a full
  Linux server. The image *is* the install path for that audience.
- **Reproducible CI.** Integration tests and Formidable-against-GiGot
  smoke tests can pin a tag instead of rebuilding from source.

### 2.2 Case against

- **Two install paths to support.** Every config change (new sealed
  store, new flag, path handling tweak) has to be re-tested against
  the container layout, not just a systemd unit. That is ongoing
  cost, not one-time.
- **State + image tension.** Everything load-bearing lives *outside*
  the image (`data/`, `repos/`, `gigot.json`). The image is almost
  pure code — which means users who get the bind-mounts wrong lose
  keys, tokens, or repos. Bad UX that the binary path doesn't have.
- **Admin bootstrap is stdin-interactive.** `gigot -add-admin alice`
  prompts for a password; that is natural in a shell, awkward in
  `docker run`. See §6.

On balance: the cost is real but bounded (one Dockerfile, one
compose file, one release-workflow job), and it unlocks a use-case
that the binary can't reach without friction. Worth doing — but
only if we treat it as a first-class artifact the same way
tarballs are, not a "community Dockerfile" that silently rots.

---

## 3. What goes in the image

The binary is the only *code* artifact, so the image is almost
entirely a runtime shell around it. Everything else — config,
keys, tokens, repos — is state and must live on a mounted volume.

### 3.1 Build strategy: multi-stage

```
# Stage 1: build
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.appVersion=${VERSION}" \
      -o /out/gigot .

# Stage 2: runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gigot /gigot
WORKDIR /var/lib/gigot
USER nonroot:nonroot
EXPOSE 3417
ENTRYPOINT ["/gigot"]
CMD ["-config", "/etc/gigot/gigot.json"]
```

Why distroless-static:

- `CGO_ENABLED=0` already gives us a fully-static binary; we don't
  need libc or a shell.
- `nonroot` (uid 65532) forces correct volume-permission discipline
  at build time rather than discovering it in production.
- ~2 MB base vs. ~7 MB Alpine; no package manager surface to patch.

Why *not* scratch: distroless gives us `/etc/ssl/certs/ca-certificates.crt`
out of the box, which GiGot needs for OAuth discovery
(`login.microsoftonline.com`, `github.com`) and for mirror-sync
pushes to `https://` upstreams. Scratch would force us to copy that
file in manually and track CA bundle updates.

### 3.2 Filesystem layout inside the image

```
/gigot                    # the binary (ENTRYPOINT)
/var/lib/gigot/           # WORKDIR; mount point for state
  data/                   # must be a volume
  repos/                  # must be a volume
/etc/gigot/gigot.json     # must be a mounted file (or bundled default)
```

Keeping `data/` and `repos/` under a single WORKDIR parent means the
operator can bind-mount *one* host directory and get the whole
persistent state of the server, which matters for backup.

### 3.3 What is deliberately *not* in the image

- **No default `gigot.json`.** Shipping a default config invites
  running against it. Instead, the image fails fast if
  `/etc/gigot/gigot.json` is missing, with an error that points at
  `gigot -init` (see §6.1). Contrast: the bare binary falls back to
  in-memory defaults, which is fine on a laptop and wrong for a
  long-lived container.
- **No default keypair.** `server.key` must be generated inside the
  mounted `data/` volume on first boot, so it survives `docker rm`.
- **No admin account.** `-add-admin` is a one-shot that the operator
  runs once against the mounted volume (§6.2).

---

## 4. Configuration contract

The config reference (README §3) already resolves all relative paths
against the directory of `gigot.json`. That single rule is what makes
containerization clean: we pick one canonical set of absolute paths
and bake them into the image's default config.

```json
{
  "server":  { "host": "0.0.0.0", "port": 3417 },
  "storage": { "repo_root": "/var/lib/gigot/repos" },
  "auth":    { "enabled": true,  "type": "token" },
  "crypto":  {
    "private_key_path": "/var/lib/gigot/data/server.key",
    "public_key_path":  "/var/lib/gigot/data/server.pub",
    "data_dir":         "/var/lib/gigot/data"
  },
  "logging": { "level": "info" }
}
```

Two things are different from the standalone README example:

- `server.host` is `0.0.0.0`, not `127.0.0.1`. Inside a container,
  `127.0.0.1` means "unreachable from outside the container."
- `auth.enabled` is `true` by default. The binary defaults to `false`
  because it targets a laptop dev loop; the image targets deployed
  use, where open `/api/*` is a footgun.

We do **not** read config from env vars. The precedent in the repo
is a single JSON config, and parallel env-var overrides become their
own maintenance burden. Instead, the operator mounts a `gigot.json`.

---

## 5. Volumes and persistence

Two named volumes, one mount point for config:

| Mount                           | Contents                          | Backup critical? |
| ------------------------------- | --------------------------------- | ---------------- |
| `/var/lib/gigot/data`           | keys, sealed stores               | **Yes — losing this = total data loss** |
| `/var/lib/gigot/repos`          | bare repos + audit chains         | Yes              |
| `/etc/gigot/gigot.json`         | config                            | Low (re-creatable) |

Both `data/` and `repos/` need to be genuinely persistent (named
volume, bind mount, or a CSI volume on k8s). A `tmpfs` mount here
loses every key, every token, every repo on `docker restart`.

`data/` and `repos/` are deliberately *separate* mounts so an
operator can snapshot repos independently (e.g. push `repos/` to a
mirror host while keeping `data/` local). They remain siblings under
`/var/lib/gigot/` so `tar czf gigot-backup.tgz /var/lib/gigot` is
one command when an operator just wants the lot.

### 5.1 The file-permissions footgun

Distroless-nonroot runs as uid 65532. Host directories created by
`docker volume create` inherit root ownership by default, so the
process can't write. Two mitigations:

- **Documented**: tell operators to `chown -R 65532:65532` the host
  dirs before first run. Simple, ugly.
- **Preferred**: ship a tiny init step in the compose/helm wrapper
  that chowns the mount on startup. But that requires root and
  defeats distroless-nonroot.

We go with the documented path and accept the ugliness. The README
section on Docker will call this out front and center.

---

## 6. First-run UX

This is where containerization bites hardest. The binary's first-run
story is "run `-init`, run `-add-admin`, run the server" — three
interactive commands in the same shell. In a container, each of
those is a separate `docker run`.

### 6.1 `gigot -init`

Not run inside the image at all. The operator writes their own
`gigot.json` (or copies the default from §4) and bind-mounts it.
`-init` is a convenience for the binary path; the container path
skips it entirely.

### 6.2 `gigot -add-admin alice`

This is unavoidable and has to be run once against the mounted
`data/` volume before the server is useful. Two options:

**Option A (recommended): one-shot container.**

```bash
docker run --rm -it \
  -v gigot-data:/var/lib/gigot/data \
  -v gigot-repos:/var/lib/gigot/repos \
  -v $(pwd)/gigot.json:/etc/gigot/gigot.json:ro \
  ghcr.io/petervdpas/gigot:latest \
  -add-admin alice
```

The `-it` is required for the password prompt. This works today
because `-add-admin` is a mutually-exclusive one-shot (README §2).

**Option B: env-var password.**

Introduce `GIGOT_ADMIN_PASSWORD` so operators can run non-interactively.
Tempting, but it breaks the existing promise that passwords never
come in through an argv/env channel, and it would bake into process
listings and docker inspect output. **Rejected.**

### 6.3 Subsequent one-shots (`-rotate-keys`, `-wipe-*`)

Same pattern as §6.2 — run a transient container with the same
volumes mounted, the flag as argv. Nothing needs to change.

---

## 7. Docker Compose

The compose file is the recommended self-host path because it
captures the volume + port + config-mount invariants in one place
that `docker` alone doesn't.

```yaml
services:
  gigot:
    image: ghcr.io/petervdpas/gigot:latest
    restart: unless-stopped
    ports:
      - "3417:3417"
    volumes:
      - gigot-data:/var/lib/gigot/data
      - gigot-repos:/var/lib/gigot/repos
      - ./gigot.json:/etc/gigot/gigot.json:ro
    healthcheck:
      test: ["CMD", "/gigot", "-healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3

volumes:
  gigot-data:
  gigot-repos:
```

Two things the compose file implies we need to add to the binary:

- **`-healthcheck` flag.** Currently no way to probe "is the server
  alive" from inside the container (distroless has no `curl` or
  `wget`). A `-healthcheck` one-shot that hits `GET /` on the
  configured `server.host:server.port` and exits 0/1 is cheap and
  avoids shelling out. Scoped separately from this doc's primary
  question.
- **Graceful shutdown on SIGTERM.** `docker stop` sends SIGTERM
  then SIGKILL after 10s. We need to verify the current server
  drains in-flight git pushes cleanly, and add a signal handler if
  it doesn't. (Open question — see §11.)

---

## 8. Image publishing

Use the existing `release.yml` tag flow. Add a job that:

- Runs on the same `v*` tag trigger.
- Builds a multi-arch image (`linux/amd64` + `linux/arm64`) via
  `docker/buildx-action`.
- Pushes to `ghcr.io/petervdpas/gigot` tagged as both
  `:v0.3.1` and `:latest`.
- Only runs after `test` + `build` pass — same gate as tarballs.

GHCR is the right registry because:

- It lives in the same GitHub account as the source, so auth is a
  single `GITHUB_TOKEN` secret.
- It's free for public images.
- `ghcr.io/petervdpas/gigot:v0.3.1` is an obvious, discoverable name.

Docker Hub is explicitly not the target — two registries means two
rate-limit surfaces, two auth flows, and two places for a tag to
drift. If someone asks for it later, add a mirror job; don't start
there.

---

## 9. Kubernetes (out of scope, but noted)

A Helm chart is a natural follow-up but is *not* part of the first
image ship. GiGot is a single-instance service (see README §12 note
on persistent admin sessions: "A true multi-instance-HA setup still
needs a shared store like Redis"). That means the k8s story is
`StatefulSet` with one replica + two PVCs — three dozen lines of
YAML, nothing that benefits from a chart yet.

Revisit when:

- Someone actually asks for a chart, *or*
- We grow a shared-state story that makes `replicas > 1` meaningful.

Until then: document the `Deployment`/`StatefulSet` shape in the
README, don't ship a chart.

---

## 10. Non-goals

- **No arm/v7 or 32-bit images.** The binary matrix already limits
  to `amd64` + `arm64`; the image follows.
- **No Windows container image.** GiGot runs on Windows as a native
  binary (`release.yml` builds `windows/amd64`), but the container
  story is Linux-only. Windows containers are a different operator
  audience and not one we're serving.
- **No "dev mode" image variant.** One image, one purpose. The
  hot-reload loop stays `go run .` on the host.
- **No docker-compose-bundled TLS.** Operators who want TLS run
  nginx / caddy / traefik in front; the image exposes plain HTTP
  on 3417 and stays that way. This matches README §11.1's stance
  on the binary.

---

## 11. Open questions

- **SIGTERM draining.** Does the current HTTP server gracefully
  close in-flight git pushes on shutdown? If not, `docker stop` can
  truncate a `git push` mid-packfile and corrupt the pending
  transaction. Needs a spike before we publish an image.
- **Healthcheck endpoint.** `GET /` currently returns the landing
  page; is a dedicated `/healthz` worthwhile, or is "HTTP 200 on
  anything" enough? Lean toward enough-for-now; add `/healthz` only
  if a container orchestrator forces the question.
- **Image scanning.** Distroless-static has close to zero CVE
  surface, but we should still wire up `trivy` or `grype` in CI so
  we notice when that changes. Separate task from this doc.

---

## 12. Ship order

If we decide to do this:

1. **Slice 1** — Dockerfile (multi-stage, distroless-nonroot),
   verified locally with bind-mounted config + volumes. README
   gets a "Docker" subsection under §11 "Deployment Modes."
2. **Slice 2** — `docker-compose.yml` in repo root, with the
   healthcheck wired once the `-healthcheck` flag exists.
3. **Slice 3** — Add a `publish-image` job to `release.yml` so
   every tagged release pushes `ghcr.io/petervdpas/gigot:<ver>`
   alongside the tarballs.
4. **Later** — Kubernetes manifest snippets in README, *only if*
   someone asks.

Slices 1 and 2 are operator-visible but don't change the Go code.
Slice 3 is release-plumbing. None of them touch the auth, policy,
or sync layers, so risk is bounded to "the container doesn't start"
rather than "production data is at risk."
