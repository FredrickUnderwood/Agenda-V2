# Observability add-on (Prometheus + Grafana)

Optional. Scrapes agenda-gateway's `/-/metrics` (request count + latency,
labeled by route/service/env/backend/method/status) and visualizes it with a
pre-provisioned "Gateway Overview" dashboard (error rate + P99 latency by
app). Independent of how/where the gateway itself is deployed.

## Bring it up

1. The shipped `prometheus.yml` assumes the gateway/control-plane are on the
   docker host (`host.docker.internal`). For any other layout — another
   machine, non-default ports — **render it from the template** instead of
   hand-editing:
   ```
   cp .env.example .env      # then edit .env
   set -a; source .env; envsubst < prometheus.yml.tmpl > prometheus.yml
   ```
   (You still need to fill the `agenda-app-metrics` bearer token — the
   `SCRAPE_TOKEN` in `.env`, which is the `observability.scrape_token` Setting.)
2. `docker compose -f deploy/observability/docker-compose.yml up -d`
3. Prometheus: http://localhost:9090 — check **Status → Targets**, the
   `agenda-gateway` job should be `UP`.
4. Grafana: http://localhost:3000 — the "Gateway Overview" dashboard is
   auto-provisioned (folder root), no login needed (anonymous Viewer).

## Embedding in the agenda-v2 frontend

The **quickstart deploy** (`deploy/quickstart`) already wires this up the right
way: Grafana is served under `/grafana` behind the web console's nginx
(`web/nginx.conf`) with `GF_SERVER_ROOT_URL`/`GF_SERVER_SERVE_FROM_SUB_PATH`,
so its port is **never published** and the console embeds panels same-origin at
`/grafana/d-solo/...`. The Monitoring tab passes the app's service name as the
dashboard's `service` template variable (`var-service`), so each app sees only
its own series, plus the environment picked in its Environment selector as
`var-env` (defaults to `prod`) — without it the panels sum prod, stage and test
together, since every expr aggregates the `env` label away.

For this **standalone add-on**, Grafana is published at the root (port 3000)
for direct access. Don't expose that port to the internet — put it behind your
own reverse proxy / network boundary. To serve it under `/grafana` like the
quickstart does, set on the grafana service:
```
GF_SERVER_ROOT_URL: "%(protocol)s://%(domain)s/grafana/"
GF_SERVER_SERVE_FROM_SUB_PATH: "true"
```
and embed:
```
<your-origin>/grafana/d-solo/agenda-gateway-overview/gateway-overview?panelId=1&var-service=<svc>&var-env=prod&theme=light&kiosk
```
`panelId=1` is the error-rate panel, `panelId=2` is P99 latency (see
`grafana/dashboards/gateway-overview.json`). `var-env` takes one env
(`prod`/`stage`/`test`) or `.*` for all of them; omitting it entirely leaves the
dashboard default, which is `.*`.

## Metrics reference

| Metric | Type | Labels |
|---|---|---|
| `gateway_requests_total` | counter | `route_key`, `service_name`, `env`, `backend`, `method`, `status_class`, `endpoint` |
| `gateway_request_duration_seconds` | histogram | `route_key`, `service_name`, `env`, `method`, `endpoint` |

`status_class` is `2xx`/`3xx`/`4xx`/`5xx` (not the raw status code, to keep
cardinality bounded). `backend` is the target's instance name (e.g.
`default`, `canary`), not the raw backend URL — kept on the counter for
per-instance traffic/errors, but dropped from the histogram (already multiplied
by `endpoint` × buckets; per-instance latency percentiles are rarely needed).

`endpoint` is the **normalized app-relative request path**, giving per-API
metrics. The gateway has no knowledge of an app's route templates, so a raw path
would explode Prometheus cardinality; it is bounded three ways: ID-looking
segments (numeric / UUID / long-hex) collapse to `:id`, depth is capped at 6
(tail → `/*`), and distinct endpoints per gateway process are capped at 200
(overflow → `/__other__`). The path is the one the backend app sees (route
prefix stripped when the route strips it), so the same endpoint reads
identically via internal and external routes.

### Example PromQL (service vs endpoint granularity)

Both levels come from the **same** metrics — aggregate the `endpoint` label away
for a service view, keep it for a per-endpoint view:

```promql
# QPS per endpoint
sum(rate(gateway_requests_total{service_name="myapp"}[1m])) by (endpoint)
# QPS for the whole service (endpoint aggregated away)
sum(rate(gateway_requests_total{service_name="myapp"}[1m]))
# 5xx error rate per endpoint
sum(rate(gateway_requests_total{service_name="myapp",status_class="5xx"}[5m])) by (endpoint)
  / sum(rate(gateway_requests_total{service_name="myapp"}[5m])) by (endpoint)
# P50 / P95 / P99 latency per endpoint
histogram_quantile(0.99, sum(rate(gateway_request_duration_seconds_bucket{service_name="myapp"}[5m])) by (le, endpoint))
```

The provisioned "Gateway Overview" dashboard has by-route panels (ids 1–2) plus
by-endpoint QPS / error-rate / P50-P95-P99 panels (ids 3–5), filterable via the
`service` and `endpoint` template variables.

## Custom app metrics

Deployed apps can define their own metrics (counters, gauges, histograms) via
`sdk/go/metric` and have them scraped alongside the gateway's own metrics —
useful for business-level instrumentation like `orders_failed_total`, which alert rules
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
   config (already included in this add-on's `prometheus.yml`, defaulting to
   `host.docker.internal:8080` — deploy/quickstart's `CONTROL_PLANE_PORT`
   default), pointing `http_sd_configs`/`__address__` at wherever the control
   plane actually listens.
6. Check Prometheus's **Status → Targets** — enabled instances should appear
   under the `agenda-app-metrics` job as `UP`.

## Roadmap: self-hosting the platform's own components

Today Grafana is reverse-proxied behind the web console's nginx. The longer-term
direction is to let agenda-gateway itself front the platform's own components
(Grafana, Prometheus, the node) as ordinary backends — "dogfooding" the gateway.
The data plane is already most of the way there: a gateway `Backend` resolves to
a plain `URL` (`internal/gateway/domain/domain.go`), so a **system static route**
(one not derived from an `ApplicationEnvTarget`, i.e. `ApplicationID=0`, and not
cleared by `SyncByApplicationEnv`) could point straight at `grafana:3000`. Once
the console frontend also sits behind the gateway, the whole platform collapses
to a single public port. Not implemented yet — tracked as a follow-up.
