# Build the agenda-gateway binary. Run from the repo root:
#   docker build -f cmd/agenda-gateway/Dockerfile -t agenda-gateway:latest .
FROM golang:1.26-alpine AS build
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /out/agenda-gateway ./cmd/agenda-gateway

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/agenda-gateway /app/agenda-gateway
# All config is via GATEWAY_* env vars (internal/gateway/config), no config file.
ENTRYPOINT ["/app/agenda-gateway"]
