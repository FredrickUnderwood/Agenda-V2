# Build the agenda-node binary. Run from the repo root:
#   docker build -f cmd/agenda-node/Dockerfile -t agenda-node:latest .
FROM golang:1.26-alpine AS build
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    CGO_ENABLED=0
WORKDIR /src
# The SDK is a separate in-tree module; copy both module manifests first so the
# download layer caches independently of source changes.
#
# The cache mounts below persist GOMODCACHE and GOCACHE across builds, which
# the layer cache cannot do: `COPY . .` sits above `go build`, so any source
# change invalidates it and every dependency gets recompiled from scratch —
# six minutes of CPU and disk on a small host, for a one-line change. The
# module cache must be mounted on both steps or this mount would shadow what
# `go mod download` put there.
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/agenda-node ./cmd/agenda-node

FROM alpine:3.20
# agenda-node shells out to git and docker compose to run deployments. When run
# as a container (vs. a host systemd unit) it needs these CLIs in the image and
# the host docker socket mounted (see docker-compose.yml). ca-certificates/tzdata
# cover its own HTTPS/timezone use (heartbeat).
RUN apk add --no-cache ca-certificates tzdata git docker-cli docker-cli-compose
WORKDIR /app
COPY --from=build /out/agenda-node /app/agenda-node
ENTRYPOINT ["/app/agenda-node", "--config", "/app/config/agenda-node.yaml"]
