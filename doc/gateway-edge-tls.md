# Gateway 内置边缘 TLS（合并 agenda-caddy）

`agenda-gateway` 现在可以自己在 `:443` 终止 TLS 并通过 ACME 自动签发证书，取代独立的
`agenda-caddy` 边缘容器。证书内核用的是 Caddy 的 [CertMagic](https://github.com/caddyserver/certmagic)
库（内嵌进 gateway 进程，不再多起一个 Caddy 进程），实现见 `internal/gateway/edgetls`。

默认**关闭**（`GATEWAY_TLS_ENABLED=false`），开启前不影响现有 `:8080` 明文数据面。

## 为什么只走 DNS-01 + ZeroSSL

沿用 `agenda-caddy` 踩过的坑（阿里云大陆节点）：

- 大陆节点对**未备案域名**在 `80` 端口强制返回备案拦截页，HTTP-01 / TLS-ALPN 校验必然失败；
- Let's Encrypt 的 ACME 接口在大陆基本连不通。

所以固定：**CA = ZeroSSL（需 EAB）**，**校验 = DNS-01（阿里云 alidns 插件自动加 `_acme-challenge` TXT）**，
全程不碰 80 端口。传播检查强制用阿里公共 DNS `223.5.5.5/223.6.6.6`（容器内嵌 DNS `127.0.0.11`
查不到权威 TXT 会僵死），并设 2m 超时兜底。这些默认值都已内置，通常无需改。

## 配置分两层：bootstrap 环境变量 + Settings 凭据

**敏感凭据（AccessKey / EAB / 邮箱）不再写进 gateway 的环境变量**，而是走控制面的
**Settings**（加密存储），由控制面周期推送给 gateway、热更新，无需重启。gateway 环境变量只留
不敏感的 bootstrap 项（决定进程/端口生命周期）。

### 1) gateway bootstrap 环境变量

| gateway 变量 | 说明 |
|---|---|
| `GATEWAY_TLS_ENABLED` | `true` 让这台 gateway 充当 TLS 边缘（绑 `:443`），默认 `false` |
| `GATEWAY_TLS_ADDR` | TLS 监听地址，默认 `:443` |
| `GATEWAY_TLS_RESOLVERS` | DNS-01 传播检查 DNS，默认 `223.5.5.5 223.6.6.6` |
| `GATEWAY_TLS_PROPAGATION_TIMEOUT` | 默认 `2m` |
| `GATEWAY_TLS_STORAGE_PATH` | 证书/账户持久化目录，默认 `/data`（须挂持久卷） |
| `GATEWAY_TLS_RECONCILE_INTERVAL` | 重新计算托管域名集合的周期，默认 `30s` |

### 2) 控制面 Settings 凭据（Settings 页 → "Gateway edge TLS" 面板）

Setting key 命名空间 `gateway.tls.`（与 `internal/service/gateway_tls_sync_service.go` 一致）：

| Setting key | secret | 对应 agenda-caddy | 说明 |
|---|:---:|---|---|
| `gateway.tls.acme_email` | | `ACME_EMAIL` | ACME 账户邮箱（必填） |
| `gateway.tls.aliyun_ak_id` | ✔ | `ALIYUN_AK_ID` | 阿里云 RAM AccessKey ID（授 `AliyunDNSFullAccess`，必填） |
| `gateway.tls.aliyun_ak_secret` | ✔ | `ALIYUN_AK_SECRET` | 阿里云 RAM AccessKey Secret（必填） |
| `gateway.tls.eab_kid` | ✔ | `ZEROSSL_EAB_KID` | ZeroSSL EAB key id |
| `gateway.tls.eab_hmac` | ✔ | `ZEROSSL_EAB_HMAC` | ZeroSSL EAB hmac key |
| `gateway.tls.acme_ca` | | Caddyfile `dir` | ACME 目录 URL，默认 ZeroSSL `https://acme.zerossl.com/v2/DV90` |
| `gateway.tls.dns_provider` | | — | 目前仅支持 `alidns`（默认） |
| `gateway.tls.static_domains` | | Caddyfile 站点地址 | 除路由 host 外要额外签发的域名，空格/逗号分隔 |

标 secret 的项在 Settings 里勾选 "Secret"，落库时用 `secret.Box` 加密。前端 Settings 页有
「Gateway edge TLS」面板，逐项列出这些 key（含 required/secret 标记），点 "Set" 直接预填新增。

推送链路：控制面 `GatewayTLSMonitor`（30s tick，`cfg.Gateway.Enabled` 时启用）读这些 Setting →
`gatewayclient.PutTLSConfig` → gateway `PUT /-/tls`（`tls.update` 权限）→ `edgetls.Manager.Reconfigure`
热更新。填齐必填项后，凭据在一个 tick 内（≤30s）生效。EAB 生成与阿里云 RAM AccessKey 的准备步骤同
`agenda-caddy` README。

## 托管哪些域名

每个 reconcile 周期（默认 30s），托管域名集合 = `gateway.tls.static_domains`（Setting）
**∪ 当前所有启用路由的 host**（`gateway_route.host`，跳过通配 `*`）。

- **API / 前后端应用域名**：本来就是 gateway 路由，在应用的 **Routes tab** 填个 `host` 即可，
  下一个周期自动纳入签发，无需额外配置。前端 Routes tab 的 Host 输入框已内置提示。
- **非 agenda 托管的固定 upstream**（如原来 agenda-caddy 直连的裸容器）：gateway 只反代注册过的
  路由，只把域名塞进 `gateway.tls.static_domains` 只会签到证书、但代理时 404（无匹配路由）。
  正确做法是把它们也纳入 agenda 当 Application 部署，或在 Routes tab 建对应路由。

> **首签窗口**：签发后台异步进行（DNS-01 传播可能要几分钟），不阻塞启动；新加域名在证书签好前
> `:443` 握手会失败，属正常，等几分钟即可。续期全自动无感。

## 部署要点

- `agenda-gateway` 容器需发布 `443`（`EXPOSE 8080 443`），并把 `/data` 挂成**持久卷**
  （证书/ACME 账户，误删可能触发 CA 速率限制）。
- Caddy 和 gateway 不能同时绑定宿主机 `80/443`。切换前先停掉旧 `agenda-caddy`
  （`docker compose -p <old-caddy-project> down`），再开启 gateway 的 `GATEWAY_TLS_ENABLED`。
- 出站需要能连 `acme.zerossl.com` 和阿里云 DNS OpenAPI；镜像已带 `ca-certificates`。

## 已知边界（后续可迭代）

- CertMagic v0.25 无 `Unmanage`：某个 host 从路由删掉后，其证书会留在缓存并继续续期，直到进程重启。对"只增不减"的边缘无害。
- 证书存储用 `FileStorage`（单节点持久卷），与旧 Caddy 一致；多副本共享存储（MySQL Storage + 分布式锁）是后续 HA 项。
- 证书到期 metric / 告警暂未接入，可在 `edgetls` 增加 gauge 后复用现有 AlertService。
