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

.PHONY: build test version clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o gigot .

test:
	go test ./...

version:
	@echo "$(VERSION)"

clean:
	rm -f gigot
