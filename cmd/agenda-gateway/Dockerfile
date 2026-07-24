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
# ca-certificates is required for outbound ACME (ZeroSSL) TLS when edge TLS is on.
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/agenda-gateway /app/agenda-gateway
# All config is via GATEWAY_* env vars (internal/gateway/config), no config file.
#
# Edge TLS (GATEWAY_TLS_ENABLED=true) makes the gateway terminate TLS on :443
# and auto-issue certs via ACME DNS-01, replacing the standalone agenda-caddy
# container. /data holds ACME accounts + issued certs and MUST be a durable
# volume — losing it can trip CA rate limits on re-issue (mirrors caddy_data).
VOLUME ["/data"]
# 8080: internal plane (admin API, metrics, service-to-service). 443: edge TLS.
EXPOSE 8080 443
ENTRYPOINT ["/app/agenda-gateway"]
