# Convenience targets for local development.
#
# CI builds via .github/workflows/release.yml and the Dockerfile. Both
# pass `-ldflags "-s -w -X main.appVersion=<v>"` so the published binary
# self-reports through `gigot -version`. This Makefile mirrors the same
# flags so a `make build` on a developer laptop produces a binary
# stamped the same way — no more `0.0.0-dev+<hash>` surprise on a
# locally-built gigot when you actually want the tag.
#
# `git describe --tags --dirty --always`:
#   - On a tag commit, clean:        v0.6.0
#   - On a tag commit, dirty tree:   v0.6.0-dirty
#   - 3 commits past v0.6.0:         v0.6.0-3-g6ef051c
#   - No tags at all:                short commit hash (the --always fallback)
#
# The leading `v` is stripped to match the convention release.yml uses
# (`${GITHUB_REF_NAME#v}`), so `gigot -version` output is consistent
# whether the binary came from CI, Docker Hub, or `make build`.

VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null | sed 's/^v//' || echo "0.0.0-dev")
LDFLAGS := -s -w -X main.appVersion=$(VERSION)

.PHONY: build test version clean assets check-assets

# Static assets are minified at build time by cmd/minify-assets and
# embedded from internal/server/assets-dist/. Source lives in
# internal/server/assets/. Both are committed so a fresh checkout can
# `go build` directly; this target re-runs generate when source has
# changed, and `check-assets` is the CI gate that verifies dist is in
# lock-step with source.
assets:
	go generate ./internal/server/...

check-assets: assets
	@if ! git diff --quiet --exit-code internal/server/assets-dist; then \
		echo "internal/server/assets-dist/ is out of date — run 'make assets' and commit the result."; \
		git --no-pager diff --stat internal/server/assets-dist; \
		exit 1; \
	fi

build: assets
	go build -trimpath -ldflags "$(LDFLAGS)" -o gigot .

test:
	go test ./...

version:
	@echo "$(VERSION)"

clean:
	rm -f gigot
