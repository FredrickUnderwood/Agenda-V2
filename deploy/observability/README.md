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
