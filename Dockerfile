# syntax=docker/dockerfile:1.7

# ---- build stage ----------------------------------------------------------
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

# ---- runtime stage --------------------------------------------------------
# Alpine (not distroless/static) because GiGot's mirror + audit paths shell
# out to git(1) at runtime — see internal/git/{refs,audit,changes}.go and
# internal/server/mirror.go. Distroless static carries no git binary.
FROM alpine:3.20

RUN apk add --no-cache git ca-certificates \
 && addgroup -g 65532 -S nonroot \
 && adduser  -u 65532 -S -G nonroot nonroot \
 && git config --system --add safe.directory '*'

COPY --from=build /out/gigot /gigot

WORKDIR /var/lib/gigot

USER nonroot:nonroot

EXPOSE 3417

# The image carries no curl/wget, so the binary's own -healthcheck flag
# is what the orchestrator probes. Settings match design doc §7.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/gigot", "-healthcheck", "-config", "/etc/gigot/gigot.json"]

ENTRYPOINT ["/gigot"]
CMD ["-config", "/etc/gigot/gigot.json"]
