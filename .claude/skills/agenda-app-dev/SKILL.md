---
name: agenda-app-dev
description: "在 agenda-v2（开源自托管 DevOps 平台）上开发并托管 Gin + React 应用的开发指南技能。当用户要把一个 Gin 后端或 React 前端部署到 agenda、接入 agenda-v2 第一方 SDK（github.com/FredrickUnderwood/agenda-v2/sdk/go 的 log / metric / alert）、写让 agenda 托管的 docker-compose、经 agenda-gateway 做服务间调用、在 agenda 控制台看日志 / 监控 / Grafana、自定义 Prometheus 打点、配置 PromQL 告警规则（AlertRule）或在业务代码里调用 SDK 发告警（飞书 / 钉钉 / 企微 / Slack / 自定义 Webhook）、看站内信时触发；也用于问'agenda 怎么部署应用'、'agenda 日志 / 指标怎么接'、'agenda 服务之间怎么调用'、'怎么在 agenda 上告警 / 打点'。注意区分：这是 agenda-v2 开源自托管平台（monorepo + 第一方 SDK + 内置 auth），不是老的 agenda-go-sdk / user-core-go-sdk / 独立 agenda-gateway 仓库 / agenda-fe 内部体系（那套用 rd-standards + docker-dev）。不用于纯排查或与 agenda 托管无关的一次性脚本。"
---

# 在 agenda-v2 上开发托管 Gin + React 应用

面向：把一个 **Gin 后端 + React 前端** 交给 **agenda-v2**（开源、自托管的 DevOps 平台）托管，并接入它的部署 / 日志 / 监控 / 网关 / 打点 / 告警能力。所有约定来自 agenda-v2 代码现状（`sdk/go`、`internal/pipeline`、`internal/gateway`、`internal/service`）。

## 0. 先分清是哪套体系（关键）

| | **本 skill：agenda-v2 开源平台** | 老内部体系（用 rd-standards + docker-dev） |
|---|---|---|
| SDK import | `github.com/FredrickUnderwood/agenda-v2/sdk/go/{log,metric,alert}` | `agenda-go-sdk/log`、`user-core-go-sdk` |
| 身份 / 权限 | 平台内置 auth（HMAC JWT，admin/member），**应用侧一般不接** | `user-core-go-sdk` RequirePerm + 前端 hasPerm |
| 网关 | monorepo 内 `agenda-gateway`（`cmd/agenda-gateway`），动态路由 | 独立 `agenda-gateway` 仓库 |
| 前端 | 你自己的 React（Vite/CRA 皆可），静态托管；`web/` 是平台自己的控制台，不是你的 app | `agenda-fe` 换皮 |

判断：import 路径带 `agenda-v2/sdk/go` 或用户说"agenda-v2 / 自托管 / 开源平台 / 控制台里创建应用"→ 用本 skill。否则用 rd-standards + docker-dev。通用的 Dockerfile / compose / 国内 registry mirror 写法仍看 **docker-dev**，本 skill 只讲 agenda-v2 特有的接入契约。

## 1. 平台拓扑与"开发者契约"

```
Client ──▶ gateway:8081 ──▶ node:7200/i/<instance> ──▶ 你的实例容器(:APP_PORT)
                                        ▲
control-plane(API+编排) ── 只经 node 中转触达实例端口（日志 / 指标 / 健康）
```

三个二进制：**control-plane**（`cmd/agenda-v2`，Web API + 部署编排大脑）、**gateway**（`cmd/agenda-gateway`，公网入口 + 动态反代 + 埋点）、**node**（`cmd/agenda-node`，常驻每台机器的 agent，执行部署 + 采日志 / 指标 + 本地反代）。

**你（应用作者）只需要交付：一个 git 仓库 + 一个 `docker-compose.yml`。** 平台在部署时会：clone/checkout → 注入一个 override compose（挂日志目录 + 注入 `AGENDA_*` 环境变量）→ `docker compose up -d --build` → 健康检查 → 同步网关路由。

### 平台注入的环境变量（你不要自己设，直接读）

部署时平台生成 `.agenda/compose.override.yml`，给每个 service 注入：

| 变量 | 值 | SDK 用途 |
|---|---|---|
| `AGENDA_APP_NAME` | Application 名 | 日志 `app` 字段 / 指标 `agenda_app` 常量标签 |
| `AGENDA_LOG_DIR` | `/var/log/agenda`（容器内，已自动挂载宿主机卷） | 日志文件落盘目录 |
| `AGENDA_ENV` | 环境（prod/test…） | 日志 `env` 字段 |
| `AGENDA_INSTANCE_NAME` | 实例名（default/blue/green…） | 日志 `instance` 字段 / 文件名 / 指标 `agenda_instance` |
| `AGENDA_SERVICE_NAME` | **compose service 名**（不是 app 名！） | 日志 `service` 字段 / 文件名 / 指标 `agenda_service` |
| `AGENDA_REPO_BRANCH` | 本次发布分支 | 可选，自行使用 |
| `AGENDA_METRICS_ADDR` | `:9464`（仅当该实例开启 metrics 时注入） | `sdk/go/metric` 监听地址 |

> 日志身份字段是 `log.Init` 自动挂的常驻字段——你**什么都不用做**，每行日志自动带 `app`/`service`/`env`/`instance`。`trace_id` 见 §5（需一行中间件）。

同时给 `docker compose up` 传 shell 环境变量供 compose 插值：`APP_PORT`（实例端口）、`APP_METRICS_PORT`（开 metrics 时）。**注意**：以 `AGENDA_` 开头的用户自定义 env 会被平台**丢弃**（防止你破坏 SDK 契约）。你的业务 env 用别的前缀。

用户自定义 env 三层合并（后者覆盖前者）：应用级 `DeployConfig.env` < 环境级 `ApplicationEnvironment.EnvVars` < 实例级 `ApplicationEnvTarget.EnvOverride`。

## 2. Gin 后端骨架（main.go）

依赖（应用自己的 `go.mod`，SDK 是独立 module，不拖平台依赖）：

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

// 自定义业务指标(打点)必须声明为【包级 var】：注册发生在 var-init 阶段，
// 早于 main()，也早于 metric.Init。放进函数里注册会晚于 Init 而丢失。
var ordersFailed = metric.NewCounterVec(prometheus.CounterOpts{
	Name: "orders_failed_total",
	Help: "Failed orders, by reason.",
}, []string{"reason"})

func main() {
	// 1) 日志：AGENDA_* 由平台注入；本地不填也能跑（只写 stderr）。
	if err := log.Init(log.Config{Level: "info"}); err != nil {
		panic(err)
	}
	defer log.Shutdown()

	// 2) 指标：开了 metrics 时平台注入 AGENDA_METRICS_ADDR=:9464，Init 会起
	//    一个独立 /metrics 监听；没开就是 no-op（指标仍注册，只是不对外服务）。
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
	r.Use(ginlog.Middleware())    // 复用/生成 X-Agenda-Trace-Id -> 每行日志自动带 trace_id，响应回写
	r.Use(ginmetric.Middleware()) // 自动 http_requests_total / http_request_duration_seconds

	// 健康检查端点：ApplicationEnvTarget 默认探 GET /healthz 期望 200。
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	r.GET("/orders/:id", func(c *gin.Context) {
		if err := doWork(c); err != nil {
			ordersFailed.WithLabelValues("db").Inc() // 打点
			log.Error(c.Request.Context(), "order failed", zap.String("id", c.Param("id")), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
	})

	// 3) 优雅退出（让 SDK flush 日志 / 关 metrics 监听）。
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

要点：
- **日志一律走 `sdk/go/log`**（`log.Info/Warn/Error/Debug(ctx, msg, zap.Field...)`），不要裸 `fmt.Print` / `log.Print`。它同时写 stderr（`docker logs`）和 `/var/log/agenda/<app>__<instance>__<service>.log`（控制台读的就是这个文件）。每行自动带 `app`/`service`/`env`/`instance`（`log.Init` 常驻字段），加了 `ginlog.Middleware()` 后还带 `trace_id`——传 `ctx`（gin 用 `c.Request.Context()`）就行。
- 容器内监听端口（示例 `:8080`）要和 compose 里 `${APP_PORT}` 映射的容器侧端口一致。
- Metrics 监听固定 `:9464`（`AGENDA_METRICS_ADDR`），compose 把它发布成 `${APP_METRICS_PORT}`。
- 想把 `/metrics` 挂到自己主路由而不起第二个监听：用 `metric.Handler()`。但 agenda 的 node 抓取默认抓 `MetricsPort`（独立端口），**推荐用 `metric.Init` 的独立 `:9464` 监听**，最省事。

## 3. docker-compose.yml（agenda 托管约定）

单后端服务：

```yaml
services:
  api:
    build: ./api                      # 或 image: registry/xxx（Dockerfile 写法见 docker-dev）
    restart: unless-stopped
    ports:
      - "${APP_PORT:-8080}:8080"          # 网关 / 健康检查经 host:APP_PORT 触达；容器侧 8080 与 main 一致
      - "${APP_METRICS_PORT:-9464}:9464"  # 开 metrics 时 node 抓 host:APP_METRICS_PORT → 容器 :9464
    # 不要自己写 AGENDA_* 环境变量，也不要手动挂 /var/log/agenda ——
    # 平台的 .agenda/compose.override.yml 会自动注入 env + 挂日志卷。
    # 业务 env（DB_DSN、下游服务地址等）通过控制台的 env 三层配置注入，别硬编码。
```

Gin + React 两种拓扑，**推荐拓扑 A**：

- **拓扑 A（推荐）：拆成两个 Application** —— `myapp-api`（Gin）和 `myapp-web`（React/nginx）。各自独立端口 / 路由 / 伸缩，指标与日志互不串。前端外部路由绑 host（如 `app.example.com`），后端可再给一条外部路由或只给 internal 路由供前端 / 其它服务调。
- **拓扑 B（简单场景）：一个 compose 两个 service**，用前端 nginx 反代 `/api` 到后端（compose 网络内 `api:8080`），只有 web 服务吃 `APP_PORT` 对外。缺点：agenda 每个 target 只有一个 `Port` / 一个 `MetricsPort`，两个 service 的指标抓取无法各自独立，多副本伸缩也受限。

React 前端（拓扑 A 的 `myapp-web`）：`npm run build` 出静态文件 → nginx 容器托管。

```yaml
services:
  web:
    build: ./web                       # 多阶段：node build → nginx 托 dist/
    restart: unless-stopped
    ports:
      - "${APP_PORT:-80}:80"
```

nginx 里让前端与 API 同源（把 `/api` 反代到后端的网关 internal 路由，避免 CORS）：

```nginx
location /api/ {
    proxy_pass http://GATEWAY_HOST:8081/svc-myapp-api/;  # 见 §4 internal 路由
}
location / {
    try_files $uri /index.html;                          # SPA 路由回退
}
```

（`GATEWAY_HOST` 用构建期或 nginx 模板变量注入，别写死。）

## 4. 经 agenda-gateway 做服务间调用

网关按 **host + path** 反代，exact-host 优先于通配 `*`，同级最长路径优先。每个 Application 在控制台 **Routes Tab** 配路由；两类：

- **external（对外）**：`host=app.example.com`，`path=/`，`strip_prefix=false` —— 公网入口。
- **internal（服务间）**：`host=*`（通配），`path=/svc-<name>`，`strip_prefix=true` —— 让别的服务用稳定前缀调你，strip 后端只收到 `/...`。

路由字段（`ApplicationGatewayRoute`）：`host` / `path_prefix` / `strip_prefix` / `backend_path` / `enabled` / `backend_mode`（`single` | `all_enabled` | `selected`）/ `instance_select_mode`（`disabled` | `enabled`）/ `instance_header`（默认 `X-Agenda-Instance`）。多实例负载：`all_enabled` 轮询；定点加权：`selected` + `backends:[{target_id,weight}]`。

**服务 A 调服务 B（agenda 托管的 Gin → Gin）**：给 B 配一条 internal 路由，A 里直接普通 HTTP 打网关：

```go
// B(myapp-api) 的 internal 路由：host=* / path=/svc-myapp-api / strip=true
base := os.Getenv("MYAPP_API_BASE") // 例：http://<gateway-host>:8081/svc-myapp-api（经 env 三层注入，别硬编码）

// 用 log.NewTransport 包一层，出站自动带上当前请求的 X-Agenda-Trace-Id ->
// A、网关、B 的日志共享同一个 trace_id（全链路可关联）。ctx 用 c.Request.Context()。
client := &http.Client{Transport: log.NewTransport(nil)}
req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, base+"/orders/123", nil)
resp, err := client.Do(req) // 网关 strip 后 B 收到 /orders/123，且 B 日志里 trace_id 与 A 一致
```

- 普通业务流量**不需要 service token**。仅"实例 pin"（定向到指定实例，`instance_select_mode=enabled` + header `X-Agenda-Instance: green`）才需要网关 token 且带 `route.invoke` 权限。
- 下游地址（`MYAPP_API_BASE`、`GATEWAY_HOST`）一律走 env 注入的配置，不写死 IP。

## 5. 日志（浏览 + 规范）

- **写**：`sdk/go/log` 自动落 `/var/log/agenda/<app>[__<instance>][__<service>][__<replica>].log`（JSON 行，lumberjack 轮转）。多实例 / 多 service 天然分文件不串。每行自动带身份字段：
  ```json
  {"level":"info","app":"myapp","service":"api","env":"prod","instance":"blue","trace_id":"9f2c…","msg":"http request","method":"GET","path":"/orders/123","status":200}
  ```
- **多副本**（`--scale`）：默认单文件；伸缩服务须显式开 `AGENDA_LOG_PER_REPLICA=1`（或给唯一 `AGENDA_REPLICA_ID`），否则多副本抢同一文件、轮转互踩。
- **看**：控制台 → 应用 → 实例 → **Logs**；或 API：
  ```
  GET /api/v1/applications/:appID/instances/:targetID/logs?tail=200
  ```
  前置：机器为 **agent 模式**（node 常驻）且该 release 已 verified。SSH 模式机器不支持读日志（已知设计限制）。
- 结构化字段用 `zap.String/Int/Error(...)`，调用时带上 `ctx`（gin 用 `c.Request.Context()`）。
- **trace（全链路关联）**：网关对每个请求注入/透传 `X-Agenda-Trace-Id`。应用侧加一行中间件把它落到 ctx，之后 `log.Info(ctx, ...)` 自动带 `trace_id`：
  - gin：`r.Use(ginlog.Middleware())`（见 §2 骨架）
  - net/http：`handler := log.TraceMiddleware(mux)`
  - 出站调用别的服务：用 `&http.Client{Transport: log.NewTransport(nil)}` + `http.NewRequestWithContext(ctx, ...)`，trace 会透传下去（见 §4），全链路同一个 `trace_id`。
  - 想再附自定义字段（如业务 request id）：设 `log.ContextFields` 钩子，与 `trace_id` 并存。

## 6. 监控（指标 + Grafana）

### 6.1 开箱即得的指标

- **HTTP 服务指标**（加了 `ginmetric.Middleware()` 就有）：`http_requests_total{route,method,status}`、`http_request_duration_seconds{route,method}`。`route` 是**路由模板**（`c.FullPath()`，如 `/orders/:id`），基数受路由表约束不随流量爆炸。附带常量标签 `agenda_app` / `agenda_instance` / `agenda_service`。
- **网关指标**（gateway 自带，`/-/metrics`）：`gateway_requests_total{route_key,service_name,env,backend,method,status_class,endpoint}`、`gateway_request_duration_seconds{route_key,service_name,env,method,endpoint}`。`endpoint` 是归一化的接口维度（数字/UUID/长 hex 段→`:id`，深度>6→`/*`，distinct>200→`/__other__`），服务维度 = PromQL 把 `endpoint` 聚合掉。

### 6.2 开启自定义指标抓取（node 中转）

平台不让 Prometheus 直连每台机器的应用端口 —— control-plane 是唯一抓取入口，经 node relay。开启步骤：

1. 实例 target 设 `metrics_enabled=true` + 一个 `metrics_port`（宿主机端口，如 19464）。**需 agent 模式机器。**
2. compose 发布 `${APP_METRICS_PORT}:9464`（平台把 metrics_port 作为 `APP_METRICS_PORT` 传给 compose）。
3. 起可观测栈：`./deploy.sh up --observability`（Prometheus + Grafana）。Prometheus 经 `http_sd` 发现 target，relabel 加上 `app`（=Application 名）/ `env` / `instance` 标签。

### 6.3 查询（PromQL）

```promql
# 自定义打点：某 app 的失败率
sum(rate(orders_failed_total{app="myapp-api"}[5m]))

# 本应用 HTTP P95（按路由）
histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{app="myapp-api"}[5m])) by (le, route))

# 网关：接口维度 QPS / 错误率 / P99
sum(rate(gateway_requests_total{service_name="myapp-api"}[1m])) by (endpoint)
sum(rate(gateway_requests_total{service_name="myapp-api",status_class="5xx"}[5m]))
  / sum(rate(gateway_requests_total{service_name="myapp-api"}[5m]))
histogram_quantile(0.99, sum(rate(gateway_request_duration_seconds_bucket{service_name="myapp-api"}[5m])) by (le, endpoint))
```

**坑**：过滤本应用用 `app="<Application 名>"`（Prometheus relabel 注入的），**不要用 `agenda_service`** —— `agenda_service` 是 **compose service 名**，多 service app 里它 ≠ 应用名。

### 6.4 Grafana

经控制台 nginx 同源反代在 `/grafana`（端口不外露）：`http://<host>:8090/grafana/`。控制台的 **Monitoring Tab** 内嵌 `/d-solo` 面板，每个 app 只看自己曲线（dashboard `service` 变量过滤）。无需再手填 Grafana URL。

## 7. 自定义打点（业务指标）

用 `sdk/go/metric` 声明，全部当**包级 var**（见 §2 注释，注册须早于 `metric.Init`）：

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

// 业务代码里：
ordersTotal.WithLabelValues("wechat").Inc()
queueDepth.Set(float64(n))
timer := prometheus.NewTimer(payLatency); defer timer.ObserveDuration()
```

API：`NewCounter/NewCounterVec`、`NewGauge/NewGaugeVec`、`NewHistogram/NewHistogramVec`。指标自动带 `agenda_app/instance/service` 常量标签。**基数纪律**：label value 必须有界（枚举/状态码/渠道），**绝不**用用户 id、订单号、原始 path 当 label。想要 Go runtime / process 指标：`metric.Init(metric.Config{EnableGoCollectors: true})`。

## 8. 告警（两条链路）

### 8.1 指标驱动 —— AlertRule（PromQL 规则引擎）

平台自建 PromQL 规则引擎（无需 Alertmanager）。建规则：

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
# 立即预览评估（绕过 30s ticker，不落状态、不真发）
curl -X POST $BASE/api/v1/alert-rules/<RID>/test -H "Authorization: Bearer $TOKEN"
```

字段：`expr`（instant PromQL，**结果向量非空 = breaching**，窗口自己在 expr 里用 `rate(...[5m])` 表达）、`for_seconds`（连续多少个评估 tick 保持 breach 才 fire，0 = 首次即 fire；**是按评估次数近似，非原生 `for:`**）、`level`（info/warning/critical）、`channels`（渠道名数组，空 = 不发外部渠道但**仍写站内信**）、`enabled`。规则 CRUD：`GET/POST /alert-rules`、`GET/PUT/DELETE /alert-rules/:id`。firing→ok 边沿自动发 `recovered` 通知。

### 8.2 业务事件驱动 —— 代码里发告警

**方式 a：SDK 自包含（`sdk/go/alert`，零平台依赖，业务内部直接调）**

```go
import "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"

ch := alert.Channel{
	Type: alert.ChannelFeishu, Name: "ops",
	WebhookURL: os.Getenv("FEISHU_WEBHOOK"), Secret: os.Getenv("FEISHU_SECRET"),
	Enabled: true,
}
results := alert.SendAll(ctx, []alert.Channel{ch}, alert.Message{
	Title: "对账失败", Content: "batch=20260725 diff=3", Level: alert.LevelCritical,
})
for _, r := range results { if r.Err != nil { log.Warn(ctx, "alert send failed", zap.String("ch", r.Channel), zap.Error(r.Err)) } }
```

支持 `ChannelFeishu`（HMAC-SHA256 签名）/ `ChannelDingTalk` / `ChannelWeCom` / `ChannelSlack` / `ChannelCustom`。`Send` 单发、`SendAll` 并发多发各返回一个 `Result`。Level：`LevelInfo/LevelWarning/LevelCritical`。**渠道 webhook / secret 由应用自己持有**（走 env 注入）。

**方式 b：复用平台集中配置的渠道 + 站内信** —— 调 control-plane：

```bash
curl -X POST $BASE/api/v1/alerts -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"对账失败","content":"...","level":"critical","channels":["ops"]}'
```

`channels` 省略 = 所有 enabled 渠道；指定名字 = 只发这些。渠道在控制台 **Settings** 配，key 形如 `alert.channel.<type>.<name>`（如 `alert.channel.feishu.ops`），value 为 JSON `{"webhook_url":"...","secret":"...","enabled":true}`（`is_secret` 加密存）。每次发送**无条件写一条站内信**（`NotificationBell` / `/inbox` 可见），外部渠道挂了也有兜底记录。

**选型**：指标类阈值告警 → §8.1 AlertRule；业务事件（对账不平、任务失败）→ §8.2；要集中管渠道密钥 + 站内信兜底选方式 b，要完全自包含、不依赖平台选方式 a。

## 9. 部署到 agenda（golden path）

控制台或 API 走一遍：

1. **建 Application**：name、repo_url、deploy_method=`docker`、deploy_config（JSON：`work_dir` / `compose_file` / `services` / `env` / `health_check`）。
2. **建环境实例 target**：env（如 prod）、instance_name、machine_id、`port`（=APP_PORT）、健康检查（默认 `GET /healthz` 期望 200）、需要监控就 `metrics_enabled` + `metrics_port`。
3. **配路由**（Routes Tab）：external 绑 host，internal 用 `/svc-<name>` + strip（见 §4）。
4. **发布**：建 release（分支）→ deploy（异步跑流水线 `git_pull → compose_up → compose_healthcheck → gateway_routes_sync`）→ 轮询 success → verify。失败 `retry` / `rollback`。
5. **验证**：流量采样、看日志、看监控、（可选）触发一次告警确认站内信到。

改代码重新上线：新 release → deploy。改实例 / 路由：`PUT /applications/:id` 是**全量 desired state**（所有实例都要带，漏了当删除；`gateway_routes` 不带=不动、`[]`=清空；路由只挂在一个代表 target、其余发 `[]`）。

## 10. 高频坑（部署 / 运行）

- **健康门控是部署时快照**：实例健康 / enabled 变化不会实时推给网关，要**重新部署**触发 `gateway_routes_sync` 才生效。
- **"健康却 502 unknown instance"**：node 内存反代注册表重启清空；控制面每 ~30s 幂等重注册，等一个 tick 或重新部署即自愈。
- **跨节点 all-or-nothing**：某节点离线会让同路由池内**其它健康节点**实例的重新部署整步失败；缓解：临时 disable 离线节点的 target。
- **指标 / 日志需 agent 模式机器**，SSH 模式不支持。
- **不要自己设 `AGENDA_*` env 或手挂 `/var/log/agenda`**，平台 override 会注入；`AGENDA_` 前缀的用户 env 会被丢弃。
- **过滤本应用指标用 `app=` 不是 `agenda_service=`**（后者是 compose service 名）。
- **`metrics_port` 冲突 / 端口复用 / 非法实例名**等业务校验目前可能返回 HTTP 500（状态码不规范，不影响功能）。

## 11. 与其它 skill 的边界

- **通用 Dockerfile / docker-compose / 国内 registry mirror / 容器连宿主机 MySQL** → **docker-dev**（本 skill 只讲 agenda-v2 特有契约：`AGENDA_*` env、`APP_PORT`/`APP_METRICS_PORT`、日志卷、路由同步）。
- **老内部体系**（`agenda-go-sdk` / `user-core-go-sdk` / `agenda-fe` / 独立 gateway 仓库）→ **rd-standards**。
- **架构设计 / 表结构 / 技术方案** → architecture / tech-design-doc。
- 本 skill 专注：把 Gin+React 交给 **agenda-v2** 托管并接入其 SDK（log/metric/alert）+ 网关 + 监控 + 告警。
