# Gateway built-in edge TLS (absorbing agenda-caddy)

`agenda-gateway` can now terminate TLS on `:443` itself and obtain certificates
automatically via ACME, replacing the standalone `agenda-caddy` edge container.
The certificate engine is Caddy's [CertMagic](https://github.com/caddyserver/certmagic)
library (embedded into the gateway process — no separate Caddy process); the
implementation lives in `internal/gateway/edgetls`.

It is **off by default** (`GATEWAY_TLS_ENABLED=false`); enabling it does not
affect the existing `:8080` plaintext data plane.

## Why DNS-01 + ZeroSSL only

Carrying over the lessons learned with `agenda-caddy` (on mainland-China Aliyun
nodes):

- Mainland nodes force an ICP-filing interception page on port `80` for any
  **unfiled domain**, so HTTP-01 / TLS-ALPN validation is guaranteed to fail.
- Let's Encrypt's ACME endpoints are essentially unreachable from mainland China.

So the defaults are fixed: **CA = ZeroSSL (requires EAB)**, **validation = DNS-01
(the Aliyun alidns plugin adds the `_acme-challenge` TXT record automatically)**,
and port 80 is never touched. Propagation checks are forced to use the Aliyun
public DNS `223.5.5.5/223.6.6.6` (the container's embedded resolver `127.0.0.11`
would hang, unable to see the authoritative TXT record), with a 2m timeout as a
backstop. These defaults are all built in and usually need no changes.

## Configuration has two layers: bootstrap env vars + Settings credentials

**Sensitive credentials (AccessKey / EAB / email) are no longer written into the
gateway's environment variables.** They go through the control plane's
**Settings** (encrypted at rest), which the control plane pushes to the gateway
periodically and hot-reloads — no restart required. The gateway's env vars keep
only the non-sensitive bootstrap items (those that govern the process/port
lifecycle).

### 1) gateway bootstrap env vars

| gateway var | description |
|---|---|
| `GATEWAY_TLS_ENABLED` | `true` makes this gateway act as a TLS edge (binds `:443`); default `false` |
| `GATEWAY_TLS_ADDR` | TLS listen address, default `:443` |
| `GATEWAY_TLS_RESOLVERS` | DNS servers for the DNS-01 propagation check, default `223.5.5.5 223.6.6.6` |
| `GATEWAY_TLS_PROPAGATION_TIMEOUT` | default `2m` |
| `GATEWAY_TLS_STORAGE_PATH` | certificate/account persistence directory, default `/data` (must be a persistent volume) |
| `GATEWAY_TLS_RECONCILE_INTERVAL` | how often the managed-domain set is recomputed, default `30s` |

### 2) control-plane Settings credentials (Settings page → "Gateway edge TLS" panel)

Setting keys under the `gateway.tls.` namespace (matching
`internal/service/gateway_tls_sync_service.go`):

| Setting key | secret | agenda-caddy equivalent | description |
|---|:---:|---|---|
| `gateway.tls.acme_email` | | `ACME_EMAIL` | ACME account email (required) |
| `gateway.tls.aliyun_ak_id` | ✔ | `ALIYUN_AK_ID` | Aliyun RAM AccessKey ID (grant `AliyunDNSFullAccess`, required) |
| `gateway.tls.aliyun_ak_secret` | ✔ | `ALIYUN_AK_SECRET` | Aliyun RAM AccessKey Secret (required) |
| `gateway.tls.eab_kid` | ✔ | `ZEROSSL_EAB_KID` | ZeroSSL EAB key id |
| `gateway.tls.eab_hmac` | ✔ | `ZEROSSL_EAB_HMAC` | ZeroSSL EAB hmac key |
| `gateway.tls.acme_ca` | | Caddyfile `dir` | ACME directory URL, default ZeroSSL `https://acme.zerossl.com/v2/DV90` |
| `gateway.tls.dns_provider` | | — | only `alidns` is supported for now (default) |
| `gateway.tls.static_domains` | | Caddyfile site addresses | extra domains to issue certs for beyond the route hosts, space/comma separated |

Items marked secret are ticked "Secret" in Settings and encrypted with
`secret.Box` when persisted. The frontend Settings page has a "Gateway edge TLS"
panel that lists these keys one by one (with required/secret markers); clicking
"Set" pre-fills a new value.

Push path: the control plane's `GatewayTLSMonitor` (30s tick, enabled when
`cfg.Gateway.Enabled`) reads these Settings → `gatewayclient.PutTLSConfig` →
gateway `PUT /-/tls` (`tls.update` permission) → `edgetls.Manager.Reconfigure`
hot-reload. Once the required items are filled in, the credentials take effect
within one tick (≤30s). EAB generation and Aliyun RAM AccessKey preparation are
the same steps as in the `agenda-caddy` README.

## Which domains get managed

Every reconcile cycle (30s by default), the managed-domain set =
`gateway.tls.static_domains` (Setting) **∪ the hosts of all currently enabled
routes** (`gateway_route.host`, skipping the wildcard `*`).

- **API / frontend-and-backend app domains**: these are already gateway routes.
  Just fill in a `host` in the app's **Routes tab** and it is picked up for
  issuance on the next cycle — no extra configuration. The frontend Routes tab's
  Host input already has an inline hint.
- **Fixed upstreams not managed by agenda** (e.g. bare containers that
  agenda-caddy used to proxy directly): the gateway only reverse-proxies
  registered routes, so shoving a domain into `gateway.tls.static_domains` alone
  will get a cert issued but 404 at proxy time (no matching route). The correct
  approach is to bring them into agenda as a deployed Application, or create a
  matching route in the Routes tab.

> **First-issuance window**: issuance happens asynchronously in the background
> (DNS-01 propagation can take a few minutes) and does not block startup. A newly
> added domain's `:443` handshake will fail until its certificate is issued —
> that is normal, just wait a few minutes. Renewal is fully automatic and
> transparent.

## Deployment notes

- The `agenda-gateway` container must publish `443` (`EXPOSE 8080 443`) and mount
  `/data` as a **persistent volume** (certificates/ACME account; deleting it by
  accident can trip CA rate limits).
- Caddy and the gateway cannot both bind host `80/443` at once. Before switching,
  stop the old `agenda-caddy` (`docker compose -p <old-caddy-project> down`),
  then enable the gateway's `GATEWAY_TLS_ENABLED`.
- Outbound connectivity to `acme.zerossl.com` and the Aliyun DNS OpenAPI is
  required; the image already ships `ca-certificates`.

## Known limitations (future iterations)

- CertMagic v0.25 has no `Unmanage`: once a host is removed from a route, its
  certificate stays in the cache and keeps renewing until the process restarts.
  Harmless for an "add-only" edge.
- Certificate storage uses `FileStorage` (single-node persistent volume), same as
  the old Caddy; shared storage across replicas (MySQL Storage + distributed
  lock) is a future HA item.
- Certificate-expiry metrics / alerts are not wired up yet; add a gauge in
  `edgetls` and reuse the existing AlertService.
