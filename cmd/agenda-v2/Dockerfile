# Build the agenda-v2 control-plane binary. Run from the repo root:
#   docker build -f cmd/agenda-v2/Dockerfile -t agenda-v2:latest .
FROM golang:1.26-alpine AS build
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    CGO_ENABLED=0
WORKDIR /src
# The SDK is a separate in-tree module; copy both module manifests first so the
# download layer caches independently of source changes.
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /out/agenda-v2 ./cmd/agenda-v2

FROM alpine:3.20
# The control plane itself only needs to reach MySQL/Redis/the gateway/nodes
# over HTTP; it does not shell out to git/docker directly as long as every
# deploy target is in agenda-node "agent" mode (see internal/runner.New —
# only "local"/"ssh" machines need git/docker in this container).
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/agenda-v2 /app/agenda-v2
ENTRYPOINT ["/app/agenda-v2", "--config", "/app/config/agenda-v2.yaml"]
