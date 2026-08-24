---
name: agenda-app-dev
description: "Guide for developing and hosting Gin + React apps on agenda-v2 (the open-source, self-hostable DevOps platform). Triggers when the user wants to deploy a Gin backend or React frontend to agenda, integrate the agenda-v2 first-party SDK (log / metric / alert under github.com/FredrickUnderwood/agenda-v2/sdk/go), write an agenda-hosted docker-compose, make service-to-service calls through agenda-gateway, view logs / monitoring / Grafana in the agenda console, add custom Prometheus instrumentation, configure PromQL alert rules (AlertRule), send alerts from business code via the SDK (Feishu / DingTalk / WeCom / Slack / custom webhook), or read the in-app inbox; also for questions like 'how do I deploy an app on agenda', 'how do I wire up agenda logs / metrics', 'how do services call each other on agenda', 'how do I alert / instrument on agenda'. Note the distinction: this is the agenda-v2 open-source self-hosted platform (monorepo + first-party SDK + built-in auth), NOT the old agenda-go-sdk / user-core-go-sdk / standalone agenda-gateway repo / agenda-fe internal stack (which uses rd-standards + docker-dev). Not for pure troubleshooting or one-off scripts unrelated to agenda hosting."
---

# Developing and hosting Gin + React apps on agenda-v2

For: handing a **Gin backend + React frontend** to **agenda-v2** (the open-source,
self-hostable DevOps platform) to host, and wiring it into the platform's deploy /
logging / monitoring / gateway / instrumentation / alerting capabilities. Every
convention here comes from the current agenda-v2 code (`sdk/go`,
`internal/pipeline`, `internal/gateway`, `internal/service`).

## 0. First, tell which stack you're on (important)

| | **This skill: agenda-v2 open-source platform** | Old internal stack (uses rd-standards + docker-dev) |
|---|---|---|
| SDK import | `github.com/FredrickUnderwood/agenda-v2/sdk/go/{log,metric,alert}` | `agenda-go-sdk/log`, `user-core-go-sdk` |
| Identity / permissions | platform built-in auth (HMAC JWT, admin/member); **app side usually doesn't integrate it** | `user-core-go-sdk` RequirePerm + frontend hasPerm |
| Gateway | in-monorepo `agenda-gateway` (`cmd/agenda-gateway`), dynamic routing | standalone `agenda-gateway` repo |
| Frontend | your own React (Vite/CRA both fine), statically hosted; `web/` is the platform's own console, NOT your app | `agenda-fe` reskin |

How to decide: an import path containing `agenda-v2/sdk/go`, or the user saying
"agenda-v2 / self-hosted / open-source platform / create an application in the
console" → use this skill. Otherwise use rd-standards + docker-dev. Generic
Dockerfile / compose / China registry-mirror conventions are still in
**docker-dev**; this skill only covers the agenda-v2-specific integration contract.

## 1. Platform topology and the "developer contract"

```
Client ──▶ gateway:8081 ──▶ node:7200/i/<instance> ──▶ your instance container(:APP_PORT)
                                        ▲
control-plane(API+orchestration) ── reaches instance ports only via the node relay (logs / metrics / health)
```

Three binaries: **control-plane** (`cmd/agenda-v2`, the Web API + deploy
orchestration brain), **gateway** (`cmd/agenda-gateway`, public entry + dynamic
reverse proxy + instrumentation), **node** (`cmd/agenda-node`, the resident
per-machine agent that runs deploys + collects logs / metrics + does local reverse
proxy).

**You (the app author) only need to deliver: one git repo + one
`docker-compose.yml`.** At deploy time the platform will: clone/checkout → inject an
override compose (mounting the log directory + injecting `AGENDA_*` env vars) →
`docker compose up -d --build` → health check → sync gateway routes.

### Environment variables the platform injects (don't set these yourself, just read them)

At deploy time the platform generates `.agenda/compose.override.yml`, injecting into
each service:

| Variable | Value | SDK use |
|---|---|---|
| `AGENDA_APP_NAME` | Application name | log `app` field / metric `agenda_app` constant label |
| `AGENDA_LOG_DIR` | `/var/log/agenda` (in-container, host volume auto-mounted) | log file output directory |
| `AGENDA_ENV` | environment (prod/test…) | log `env` field |
| `AGENDA_INSTANCE_NAME` | instance name (default/blue/green…) | log `instance` field / filename / metric `agenda_instance` |
| `AGENDA_SERVICE_NAME` | **compose service name** (not the app name!) | log `service` field / filename / metric `agenda_service` |
| `AGENDA_REPO_BRANCH` | the branch of this release | optional, use as you like |
| `AGENDA_METRICS_ADDR` | `:9464` (injected only when metrics is enabled for this instance) | `sdk/go/metric` listen address |

> The log identity fields are resident fields attached automatically by `log.Init`
> — you **do nothing**, every log line automatically carries
> `app`/`service`/`env`/`instance`. `trace_id` is in §5 (needs one middleware line).

It also passes shell env vars to `docker compose up` for compose interpolation:
`APP_PORT` (instance port), `APP_METRICS_PORT` (when metrics is on). **Note**:
user-defined env vars starting with `AGENDA_` are **discarded** by the platform (to
stop you breaking the SDK contract). Use a different prefix for your business env.

User-defined env vars are configured **per environment**, in the console's
**Env vars** tab on the application page: one row per variable, one column per
environment (Prod / Stage / Test). A blank cell deploys as an empty value —
environments do not inherit from each other. Changes take effect on the next
deploy, not on save.

Under the hood they merge in three layers (later overrides earlier): the legacy
app-level `DeployConfig.env` baseline (no longer editable in the UI; migrated
into Prod on upgrade) < env-level `ApplicationEnvironment.EnvVars` (what the tab
writes) < instance-level `ApplicationEnvTarget.EnvOverride` (API only).

## 2. Gin backend skeleton (main.go)

Dependency (your app's own `go.mod`; the SDK is a standalone module and doesn't
drag in platform deps):

```
go get github.com/FredrickUnderwood/agenda-v2/sdk/go
```

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log/ginlog"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/metric"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/metric/ginmetric"
)

// Custom business metrics (instrumentation) MUST be declared as [package-level var]:
// registration happens during var-init, before main() and before metric.Init.
// Registering inside a function runs after Init and gets lost.
var ordersFailed = metric.NewCounterVec(prometheus.CounterOpts{
	Name: "orders_failed_total",
	Help: "Failed orders, by reason.",
}, []string{"reason"})

func main() {
	// 1) Logging: AGENDA_* is injected by the platform; runs locally without them too (writes stderr only).
	if err := log.Init(log.Config{Level: "info"}); err != nil {
		panic(err)
	}
	defer log.Shutdown()

	// 2) Metrics: when metrics is on, the platform injects AGENDA_METRICS_ADDR=:9464 and Init starts
	//    a dedicated /metrics listener; when off it's a no-op (metrics still register, just not served).
	if err := metric.Init(metric.Config{}); err != nil {
		log.Error(context.Background(), "metric init failed", zap.Error(err))
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = metric.Shutdown(ctx)
	}()

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginlog.Middleware())    // reuse/generate X-Agenda-Trace-Id -> every log line auto-carries trace_id, echoed on the response
	r.Use(ginmetric.Middleware()) // automatic http_requests_total / http_request_duration_seconds

	// Health-check endpoint: ApplicationEnvTarget probes GET /healthz expecting 200 by default.
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	r.GET("/orders/:id", func(c *gin.Context) {
		if err := doWork(c); err != nil {
			ordersFailed.WithLabelValues("db").Inc() // instrumentation
			log.Error(c.Request.Context(), "order failed", zap.String("id", c.Param("id")), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
	})

	// 3) Graceful shutdown (let the SDK flush logs / close the metrics listener).
	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(context.Background(), "http serve", zap.Error(err))
		}
	}()
	log.Info(context.Background(), "server started", zap.String("addr", ":8080"))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
```

Key points:
- **Always log through `sdk/go/log`** (`log.Info/Warn/Error/Debug(ctx, msg, zap.Field...)`),
  never bare `fmt.Print` / `log.Print`. It writes both stderr (`docker logs`) and
  `/var/log/agenda/<app>__<instance>__<service>.log` (the file the console reads).
  Every line auto-carries `app`/`service`/`env`/`instance` (`log.Init` resident
  fields); with `ginlog.Middleware()` added it also carries `trace_id` — just pass
  `ctx` (in gin, `c.Request.Context()`).
- The in-container listen port (`:8080` in the example) must match the container-side
  port mapped by `${APP_PORT}` in compose.
- The metrics listener is fixed at `:9464` (`AGENDA_METRICS_ADDR`); compose publishes
  it as `${APP_METRICS_PORT}`.
- To mount `/metrics` on your own main router instead of a second listener: use
  `metric.Handler()`. But agenda's node scrape defaults to `MetricsPort` (a dedicated
  port), so **the dedicated `:9464` listener from `metric.Init` is recommended** —
  least hassle.

## 3. docker-compose.yml (agenda hosting conventions)

Single backend service:

```yaml
services:
  api:
    build: ./api                      # or image: registry/xxx (Dockerfile conventions in docker-dev)
    restart: unless-stopped
    ports:
      - "${APP_PORT:-8080}:8080"          # gateway / health check reach it via host:APP_PORT; container-side 8080 matches main
      - "${APP_METRICS_PORT:-9464}:9464"  # when metrics is on, node scrapes host:APP_METRICS_PORT -> container :9464
    # Do NOT write AGENDA_* env vars yourself, and do NOT manually mount /var/log/agenda —
    # the platform's .agenda/compose.override.yml auto-injects env + mounts the log volume.
    # Business env (DB_DSN, downstream service addresses, etc.) is injected via the console's
    # three-layer env config; don't hard-code it.
    #
    # Optional, and ONLY needed if you plan to turn on deploy_config's
    # health_check.require_healthy. Without a healthcheck: block the container's
    # .State.Health is permanently "none", and require_healthy then fails every
    # deploy (see §10). The test command must exist in your image — alpine ships
    # busybox wget; distroless has neither wget nor curl.
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 5s
      timeout: 3s
      retries: 12
      start_period: 15s
```

Two topologies for Gin + React; **topology A recommended**:

- **Topology A (recommended): split into two Applications** — `myapp-api` (Gin) and
  `myapp-web` (React/nginx). Each gets its own port / route / scaling, and their
  metrics and logs don't bleed into each other. The frontend's external route binds a
  host (e.g. `app.example.com`); the backend can get its own external route or just an
  internal route for the frontend / other services to call.
- **Topology B (simple cases): one compose, two services**, with the frontend nginx
  reverse-proxying `/api` to the backend (`api:8080` inside the compose network); only
  the web service takes `APP_PORT` externally. Downside: each agenda target has only
  one `Port` / one `MetricsPort`, so the two services' metric scraping can't be
  independent and multi-replica scaling is limited.

React frontend (topology A's `myapp-web`): `npm run build` produces static files →
nginx container serves them.

```yaml
services:
  web:
    build: ./web                       # multi-stage: node build -> nginx serves dist/
    restart: unless-stopped
    ports:
      - "${APP_PORT:-80}:80"
```

In nginx, keep the frontend and API same-origin (reverse-proxy `/api` to the
backend's internal gateway route, avoiding CORS):

```nginx
location /api/ {
    proxy_pass http://GATEWAY_HOST:8081/svc-myapp-api/;  # see §4 internal routes
}
location / {
    try_files $uri /index.html;                          # SPA route fallback
}
```

(Inject `GATEWAY_HOST` via a build-time or nginx template variable, don't hard-code it.)

## 4. Service-to-service calls through agenda-gateway

The gateway reverse-proxies by **host + path**, exact-host beating the wildcard `*`,
and the longest path winning at the same level. Each Application configures routes in
the console **Routes Tab**; two kinds:

- **external**: `host=app.example.com`, `path=/`, `strip_prefix=false` — the public
  entry.
- **internal (service-to-service)**: `host=*` (wildcard), `path=/svc-<name>`,
  `strip_prefix=true` — lets other services call you via a stable prefix; after
  stripping, the backend only sees `/...`.

Route fields (`ApplicationGatewayRoute`): `host` / `path_prefix` / `strip_prefix` /
`backend_path` / `enabled` / `backend_mode` (`single` | `all_enabled` | `selected`) /
`instance_select_mode` (`disabled` | `enabled`) / `instance_header` (default
`X-Agenda-Instance`). Multi-instance load: `all_enabled` round-robins; targeted
weighting: `selected` + `backends:[{target_id,weight}]`.

**Service A calling service B (agenda-hosted Gin → Gin)**: give B an internal route,
and A just makes an ordinary HTTP call to the gateway:

```go
// B(myapp-api)'s internal route: host=* / path=/svc-myapp-api / strip=true
base := os.Getenv("MYAPP_API_BASE") // e.g. http://<gateway-host>:8081/svc-myapp-api (injected via three-layer env, don't hard-code)

// Wrap with log.NewTransport so outbound requests auto-carry the current request's X-Agenda-Trace-Id ->
// A, the gateway, and B share the same trace_id (fully correlatable). Use c.Request.Context() for ctx.
client := &http.Client{Transport: log.NewTransport(nil)}
req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, base+"/orders/123", nil)
resp, err := client.Do(req) // after the gateway strips, B receives /orders/123, and B's logs carry the same trace_id as A
```

- Ordinary business traffic **needs no service token**. Only "instance pinning" (route
  to a specific instance, `instance_select_mode=enabled` + header
  `X-Agenda-Instance: green`) needs a gateway token with the `route.invoke` permission.
- Downstream addresses (`MYAPP_API_BASE`, `GATEWAY_HOST`) always come from env-injected
  config, never a hard-coded IP.

## 5. Logging (viewing + conventions)

- **Write**: `sdk/go/log` auto-writes
  `/var/log/agenda/<app>[__<instance>][__<service>][__<replica>].log` (JSON lines,
  lumberjack rotation). Multiple instances / services naturally split into separate
  files without bleed. Every line auto-carries identity fields:
  ```json
  {"level":"info","app":"myapp","service":"api","env":"prod","instance":"blue","trace_id":"9f2c…","msg":"http request","method":"GET","path":"/orders/123","status":200}
  ```
- **Multiple replicas** (`--scale`): single file by default; a scaled service must
  explicitly set `AGENDA_LOG_PER_REPLICA=1` (or give a unique `AGENDA_REPLICA_ID`),
  otherwise replicas fight over one file and rotation clobbers itself.
- **View**: console → application → instance → **Logs**; or the API:
  ```
  GET /api/v1/applications/:appID/instances/:targetID/logs?tail=200
  ```
  Prerequisite: the machine is in **agent mode** (node resident) and the release is
  verified. SSH-mode machines don't support reading logs (a known design limitation).
- Use `zap.String/Int/Error(...)` for structured fields, and pass `ctx` (in gin,
  `c.Request.Context()`).
- **trace (full-chain correlation)**: the gateway injects/propagates
  `X-Agenda-Trace-Id` per request. On the app side, add one middleware line to land it
  into ctx; then `log.Info(ctx, ...)` auto-carries `trace_id`:
  - gin: `r.Use(ginlog.Middleware())` (see the §2 skeleton)
  - net/http: `handler := log.TraceMiddleware(mux)`
  - outbound calls to other services: use `&http.Client{Transport: log.NewTransport(nil)}`
    + `http.NewRequestWithContext(ctx, ...)`, and the trace propagates downstream (see
    §4), same `trace_id` across the whole chain.
  - to attach extra custom fields (e.g. a business request id): set the
    `log.ContextFields` hook, coexisting with `trace_id`.

## 6. Monitoring (metrics + Grafana)

### 6.1 Metrics you get out of the box

- **HTTP service metrics** (present once `ginmetric.Middleware()` is added):
  `http_requests_total{route,method,status}`,
  `http_request_duration_seconds{route,method}`. `route` is the **route template**
  (`c.FullPath()`, e.g. `/orders/:id`), so cardinality is bounded by the route table
  and doesn't explode with traffic. Accompanied by constant labels `agenda_app` /
  `agenda_instance` / `agenda_service`.
- **Gateway metrics** (built into the gateway, `/-/metrics`):
  `gateway_requests_total{route_key,service_name,env,backend,method,status_class,endpoint}`,
  `gateway_request_duration_seconds{route_key,service_name,env,method,endpoint}`.
  `endpoint` is the normalized API dimension (numbers/UUIDs/long hex segments → `:id`,
  depth>6 → `/*`, distinct>200 → `/__other__`); the service dimension = aggregate
  `endpoint` away in PromQL.

### 6.2 Enabling custom-metric scraping (via the node relay)

The platform doesn't let Prometheus connect directly to each machine's app ports —
the control plane is the sole scrape entry, via the node relay. Steps to enable:

1. Set the instance target's `metrics_enabled=true` + a `metrics_port` (host port, e.g.
   19464). **Requires an agent-mode machine.**
2. compose publishes `${APP_METRICS_PORT}:9464` (the platform passes metrics_port as
   `APP_METRICS_PORT` to compose).
3. Bring up the observability stack: `./deploy.sh up --observability` (Prometheus +
   Grafana). Prometheus discovers targets via `http_sd` and relabels in `app`
   (= Application name) / `env` / `instance` labels.

### 6.3 Querying (PromQL)

```promql
# Custom instrumentation: an app's failure rate
sum(rate(orders_failed_total{app="myapp-api"}[5m]))

# This app's HTTP P95 (by route)
histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{app="myapp-api"}[5m])) by (le, route))

# Gateway: per-endpoint QPS / error rate / P99
sum(rate(gateway_requests_total{service_name="myapp-api"}[1m])) by (endpoint)
sum(rate(gateway_requests_total{service_name="myapp-api",status_class="5xx"}[5m]))
  / sum(rate(gateway_requests_total{service_name="myapp-api"}[5m]))
histogram_quantile(0.99, sum(rate(gateway_request_duration_seconds_bucket{service_name="myapp-api"}[5m])) by (le, endpoint))
```

**Trap**: filter your own app with `app="<Application name>"` (injected by Prometheus
relabel); **don't use `agenda_service`** — `agenda_service` is the **compose service
name**, which in a multi-service app ≠ the app name.

### 6.4 Grafana

Reverse-proxied same-origin under the console's nginx at `/grafana` (the port isn't
exposed): `http://<host>:8090/grafana/`. The console's **Monitoring Tab** embeds
`/d-solo` panels; each app sees only its own curves (filtered by the dashboard
`service` variable), for one environment at a time (the **Environment** picker next
to the time range, dashboard `env` variable, default `prod` — pick "All envs" to
sum them). No need to fill in a Grafana URL.

## 7. Custom instrumentation (business metrics)

Declare with `sdk/go/metric`, all as **package-level vars** (see the §2 comment,
registration must precede `metric.Init`):

```go
var (
	ordersTotal = metric.NewCounterVec(prometheus.CounterOpts{
		Name: "orders_total", Help: "Orders created, by channel.",
	}, []string{"channel"})

	queueDepth = metric.NewGauge(prometheus.GaugeOpts{
		Name: "job_queue_depth", Help: "Pending jobs.",
	})

	payLatency = metric.NewHistogram(prometheus.HistogramOpts{
		Name: "payment_seconds", Help: "Payment latency.",
		Buckets: prometheus.DefBuckets,
	})
)

// In business code:
ordersTotal.WithLabelValues("wechat").Inc()
queueDepth.Set(float64(n))
timer := prometheus.NewTimer(payLatency); defer timer.ObserveDuration()
```

API: `NewCounter/NewCounterVec`, `NewGauge/NewGaugeVec`,
`NewHistogram/NewHistogramVec`. Metrics auto-carry the `agenda_app/instance/service`
constant labels. **Cardinality discipline**: label values must be bounded
(enum/status code/channel); **never** use a user id, order number, or raw path as a
label. For Go runtime / process metrics:
`metric.Init(metric.Config{EnableGoCollectors: true})`.

## 8. Alerting (two paths)

### 8.1 Metric-driven — AlertRule (PromQL rule engine)

The platform has a self-built PromQL rule engine (no Alertmanager needed). Create a
rule:

```bash
curl -X POST $BASE/api/v1/alert-rules -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{
    "name": "orders-fail-spike",
    "expr": "sum(rate(orders_failed_total{app=\"myapp-api\"}[5m])) > 1",
    "for_seconds": 0,
    "level": "warning",
    "channels": [],
    "enabled": true
  }'
# Preview an evaluation immediately (bypasses the 30s ticker, no state persisted, nothing actually sent)
curl -X POST $BASE/api/v1/alert-rules/<RID>/test -H "Authorization: Bearer $TOKEN"
```

Fields: `expr` (instant PromQL, **a non-empty result vector = breaching**; express the
window in the expr itself via `rate(...[5m])`), `for_seconds` (how many consecutive
evaluation ticks must stay breaching before firing, 0 = fire on the first;
**approximated by evaluation count, not a native `for:`**), `level`
(info/warning/critical), `channels` (array of channel names, empty = don't send to
external channels but **still write the in-app inbox**), `enabled`. Rule CRUD:
`GET/POST /alert-rules`, `GET/PUT/DELETE /alert-rules/:id`. The firing→ok edge
auto-sends a `recovered` notification.

### 8.2 Business-event driven — send alerts from code

**Option a: SDK self-contained (`sdk/go/alert`, zero platform dependency, called
directly from business code)**

```go
import "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"

ch := alert.Channel{
	Type: alert.ChannelFeishu, Name: "ops",
	WebhookURL: os.Getenv("FEISHU_WEBHOOK"), Secret: os.Getenv("FEISHU_SECRET"),
	Enabled: true,
}
results := alert.SendAll(ctx, []alert.Channel{ch}, alert.Message{
	Title: "reconciliation failed", Content: "batch=20260725 diff=3", Level: alert.LevelCritical,
})
for _, r := range results { if r.Err != nil { log.Warn(ctx, "alert send failed", zap.String("ch", r.Channel), zap.Error(r.Err)) } }
```

Supports `ChannelFeishu` (HMAC-SHA256 signed) / `ChannelDingTalk` / `ChannelWeCom` /
`ChannelSlack` / `ChannelCustom`. `Send` sends to one, `SendAll` sends to many
concurrently, each returning a `Result`. Levels:
`LevelInfo/LevelWarning/LevelCritical`. **The channel webhook / secret is held by the
app itself** (via env injection).

**Option b: reuse the platform's centrally-configured channels + in-app inbox** — call
the control plane:

```bash
curl -X POST $BASE/api/v1/alerts -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"reconciliation failed","content":"...","level":"critical","channels":["ops"]}'
```

Omitting `channels` = all enabled channels; naming them = only those. Channels are
configured in the console **Settings**, with keys like
`alert.channel.<type>.<name>` (e.g. `alert.channel.feishu.ops`) and a JSON value
`{"webhook_url":"...","secret":"...","enabled":true}` (`is_secret` stored encrypted).
Every send **unconditionally writes one in-app notification** (visible in the
`NotificationBell` / `/inbox`), so there's a fallback record even if an external
channel is down.

**Choosing**: metric threshold alerts → §8.1 AlertRule; business events
(reconciliation mismatch, task failure) → §8.2; want centrally-managed channel secrets
+ in-app-inbox fallback, pick option b; want fully self-contained with no platform
dependency, pick option a.

## 9. Deploying to agenda (golden path)

Walk through it in the console or via the API:

1. **Create the Application**: name, repo_url, deploy_method=`docker`, deploy_config
   (JSON: `work_dir` / `compose_file` / `services` / `env` / `health_check`). Here
   `health_check` configures the **container-level** check the pipeline runs
   (`enabled`, `require_healthy`, `timeout_seconds`, `interval_seconds`) — a
   different thing from step 2's HTTP probe; see §10.
2. **Create an environment instance target**: env (e.g. prod), instance_name,
   machine_id, `port` (=APP_PORT), health check (default `GET /healthz` expecting 200 —
   this is the **HTTP** probe, and what the Application page shows),
   and for monitoring `metrics_enabled` + `metrics_port`.
3. **Configure routes** (Routes Tab): external binds a host, internal uses `/svc-<name>`
   + strip (see §4).
4. **Release**: create a release (branch) → deploy (async pipeline
   `git_pull → compose_up → compose_healthcheck → gateway_routes_sync`) → poll to
   success → verify. On failure `retry` / `rollback`.
5. **Verify**: sample traffic, check logs, check monitoring, (optionally) trigger an
   alert to confirm the in-app inbox receives it.

Redeploy after a code change: new release → deploy. Change instances / routes:
`PUT /applications/:id` is the **full desired state** (all instances must be included;
an omitted one counts as deleted; `gateway_routes` omitted = unchanged, `[]` = cleared;
routes attach to one representative target, send `[]` for the rest).

## 10. Common traps (deploy / runtime)

- **`compose_healthcheck` is NOT the instance health check — two independent
  mechanisms**: the pipeline step only reads `docker inspect`'s `.State.Status` /
  `.State.Health` (which comes from compose's `healthcheck:` or the Dockerfile's
  `HEALTHCHECK`) and **issues no HTTP request at all**; the instance health check is the
  control plane's HTTP `GET /healthz` probe every ~15s, and that is what the Application
  page displays. With no `healthcheck:` declared, `.State.Health` is permanently `none` —
  so turning on `health_check.require_healthy` (the UI switch labelled "Fail deploy if not
  healthy") makes every deploy fail with `compose healthcheck failed: <service> has no
  Docker healthcheck` while the Application page keeps showing healthy. Either leave
  `require_healthy` off, or declare a `healthcheck:` in compose (see §3).
- **Health gating is a deploy-time snapshot**: instance health / enabled changes aren't
  pushed to the gateway in real time; you must **redeploy** to trigger
  `gateway_routes_sync` for them to take effect.
- **"Healthy but 502 unknown instance"**: the node's in-memory reverse-proxy registry is
  cleared on restart; the control plane idempotently re-registers every ~30s, so wait
  one tick or redeploy and it self-heals.
- **Cross-node all-or-nothing**: one node being offline makes a redeploy of instances of
  **other healthy nodes** in the same route pool fail the whole step; mitigation:
  temporarily disable the offline node's target.
- **Metrics / logs require an agent-mode machine**; SSH mode is unsupported.
- **Don't set `AGENDA_*` env yourself or manually mount `/var/log/agenda`**; the
  platform override injects them; user env with an `AGENDA_` prefix is discarded.
- **Filter your own app's metrics with `app=`, not `agenda_service=`** (the latter is
  the compose service name).
- **`metrics_port` conflicts / port reuse / invalid instance names** and similar
  business validations may currently return HTTP 500 (a non-standard status code that
  doesn't affect functionality).

## 11. Boundaries with other skills

- **Generic Dockerfile / docker-compose / China registry mirror / container-to-host
  MySQL** → **docker-dev** (this skill only covers agenda-v2-specific contracts:
  `AGENDA_*` env, `APP_PORT`/`APP_METRICS_PORT`, the log volume, route sync).
- **Old internal stack** (`agenda-go-sdk` / `user-core-go-sdk` / `agenda-fe` / the
  standalone gateway repo) → **rd-standards**.
- **Architecture design / table schema / technical proposals** → architecture /
  tech-design-doc.
- This skill focuses on: handing Gin+React to **agenda-v2** to host and wiring in its
  SDK (log/metric/alert) + gateway + monitoring + alerting.
```
