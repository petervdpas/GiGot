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
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gigot /gigot

WORKDIR /var/lib/gigot

USER nonroot:nonroot

EXPOSE 3417

ENTRYPOINT ["/gigot"]
CMD ["-config", "/etc/gigot/gigot.json"]
