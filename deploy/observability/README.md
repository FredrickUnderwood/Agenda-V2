# Observability add-on (Prometheus + Grafana)

Optional. Scrapes agenda-gateway's `/-/metrics` (request count + latency,
labeled by route/service/env/backend/method/status) and visualizes it with a
pre-provisioned "Gateway Overview" dashboard (error rate + P99 latency by
app). Independent of how/where the gateway itself is deployed.

## Bring it up

1. Edit `prometheus.yml`'s target if your gateway isn't reachable at
   `host.docker.internal:8080` (e.g. it's on another machine, or
   `GATEWAY_ADDR` is non-default).
2. `docker compose -f deploy/observability/docker-compose.yml up -d`
3. Prometheus: http://localhost:9090 — check **Status → Targets**, the
   `agenda-gateway` job should be `UP`.
4. Grafana: http://localhost:3000 — the "Gateway Overview" dashboard is
   auto-provisioned (folder root), no login needed (anonymous Viewer).

## Embedding in the agenda-v2 frontend

Grafana runs with `GF_AUTH_ANONYMOUS_ENABLED` + `GF_SECURITY_ALLOW_EMBEDDING`
so its panels can be iframed directly, e.g.:

```
http://<grafana-host>:3000/d-solo/agenda-gateway-overview/gateway-overview?panelId=1&theme=light&kiosk
```

`panelId=1` is the error-rate panel, `panelId=2` is P99 latency (see
`grafana/dashboards/gateway-overview.json`). Don't expose Grafana's port
directly to the internet — reverse-proxy it (through the gateway itself, or
your own ingress) the same way you would any other internal service.

## Metrics reference

| Metric | Type | Labels |
|---|---|---|
| `gateway_requests_total` | counter | `route_key`, `service_name`, `env`, `backend`, `method`, `status_class` |
| `gateway_request_duration_seconds` | histogram | `route_key`, `service_name`, `env`, `backend`, `method` |

`status_class` is `2xx`/`3xx`/`4xx`/`5xx` (not the raw status code, to keep
cardinality bounded). `backend` is the target's instance name (e.g.
`default`, `canary`), not the raw backend URL.

## Custom app metrics

Deployed apps can define their own metrics (counters, gauges, histograms) via
`sdk/go/metric` and have them scraped alongside the gateway's own metrics —
useful for business-level "打点" like `orders_failed_total`, which alert rules
(see below) can then fire on.

The control plane, not Prometheus, is what reaches each app instance: it
relays every scrape through that instance's agenda-node over the same
authenticated channel used for log reading, so Prometheus never needs direct
network access to app ports on every deploy machine. Only agent-mode machines
support this (same requirement application log reading already has).

Setup:
1. In the app's code: `metric.Init(metric.Config{})` once at startup, then
   define metrics with `metric.NewCounterVec`/`NewGaugeVec`/`NewHistogramVec`
   — see `sdk/go/metric`.
2. In the app's own `docker-compose.yml`, publish the metrics port:
   `ports: ["${APP_METRICS_PORT:-9464}:9464"]` (mirrors the existing
   `${APP_PORT}` convention).
3. In the agenda-v2 console, enable "Metrics" on the target's Instance
   config and set its host port (this becomes `APP_METRICS_PORT`).
4. Configure two agenda-v2 Settings (Settings page, or
   `PUT /api/v1/settings/:key`): `observability.prometheus_url` (this
   Prometheus's own base URL, e.g. `http://<host>:9090`) and
   `observability.scrape_token` (any random secret — the bearer token
   Prometheus presents back to the control plane; mark it `is_secret`).
5. Add the `agenda-app-metrics` job from `prometheus.yml` to your Prometheus
   config (already included in this add-on's `prometheus.yml`), pointing
   `http_sd_configs`/`__address__` at wherever the control plane actually
   listens.
6. Check Prometheus's **Status → Targets** — enabled instances should appear
   under the `agenda-app-metrics` job as `UP`.
