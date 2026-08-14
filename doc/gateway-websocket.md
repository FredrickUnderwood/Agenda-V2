# WebSocket through agenda-gateway

The gateway carries WebSocket connections end to end:

```
browser ──ws/wss──▶ agenda-gateway ──▶ agenda-node /i/<instance> ──▶ your app
```

Both proxy hops complete the HTTP handshake and then become an opaque byte
tunnel. Neither one parses WebSocket frames, so anything you can send over a
WebSocket you can send through agenda — text, binary, compression extensions,
subprotocols.

WebSocket is **off by default on every route**. A route has to opt in.

## Enabling it on a route

Console → your application → **Routes** → edit a route → **Protocol upgrade** →
`WebSocket`.

| Field | Meaning | Default |
|---|---|---|
| Protocol upgrade | `None` rejects Upgrade requests; `WebSocket` allows RFC 6455 | `None` |
| Request timeout (ms) | Total time an ordinary HTTP request may take. **Never applied to a WebSocket.** | gateway default (30s) |
| WebSocket idle timeout (ms) | Closes a tunnel after this long with no bytes in either direction. `-1` disables it. | gateway default (5 min) |
| Max WebSocket connections | Concurrent tunnels allowed on this route. `0` = unlimited | `0` |
| Allowed origins | Comma-separated browser `Origin` allowlist. Empty = any origin | empty |

Route config lives in the control plane and is pushed to the gateway on every
deploy, so it survives releases and rollbacks. Editing it in the console takes
effect on the next deploy of that app.

### Why opt-in rather than always on

An upgraded request is not a request any more. It escapes the request timeout,
holds a connection on the gateway *and* one on the node relay for its whole
life, and cannot be load-balanced away once established. Making that reachable
by simply sending an `Upgrade` header on any route would mean any caller could
convert a cheap endpoint into a permanent resource commitment. This mirrors
Envoy's per-route `upgrade_configs` rather than Traefik's always-on behaviour.

Only `Upgrade: websocket` is accepted. Any other token (`h2c`, etc.) gets a
`501`. HTTP/2 Extended CONNECT (RFC 8441) is not supported — a WebSocket client
must speak HTTP/1.1 to the gateway, which is what every browser does today.

### Timeouts: total vs idle

The route's request timeout is a *maximum lifetime*. On a long-lived connection
that is not a timeout at all — it is a scheduled disconnect, and setting it to
an hour just moves the disconnect to an hour. So the gateway applies:

- **plain HTTP** → total request timeout (unchanged behaviour)
- **WebSocket** → no total deadline; instead an **idle timeout** on the tunnel,
  plus a 5s dial timeout and a 10s handshake (response header) timeout so a
  black-holed backend can't leave a handshake hanging.

The idle timeout is refreshed by traffic in *either* direction, so a server
pushing events keeps its own connection alive. If you disable it (`-1`), your
app must send Ping frames — otherwise a peer that vanished without a TCP FIN
(laptop lid closed, NAT rebind) holds its slot forever.

## Gateway-wide limits

Per-route settings can't express the gateway's own capacity, so these are env
vars on the gateway process:

| Env var | Meaning | Default |
|---|---|---|
| `GATEWAY_WS_IDLE_TIMEOUT` | Idle timeout for routes that don't set one | `5m` |
| `GATEWAY_WS_MAX_CONNECTIONS` | Cap across all routes (0 = off) | `0` |
| `GATEWAY_WS_MAX_CONNECTIONS_PER_IP` | Cap per peer address (0 = off) | `0` |
| `GATEWAY_WS_HANDSHAKE_RATE` | Handshakes/second across the gateway (0 = off) | `0` |
| `GATEWAY_WS_HANDSHAKE_BURST` | Burst allowance for the above | one second's worth |
| `GATEWAY_WS_DIAL_TIMEOUT` | Upstream TCP/TLS dial timeout | `5s` |
| `GATEWAY_WS_HANDSHAKE_TIMEOUT` | Wait for the backend's 101 | `10s` |
| `GATEWAY_WS_DRAIN_TIMEOUT` | Restart: wait for tunnels before cutting them | `30s` |
| `GATEWAY_MAX_HEADER_BYTES` | Request header cap on the data plane | `64KiB` |

The per-IP cap keys on the TCP peer address, deliberately ignoring
`X-Forwarded-For` (a caller-controlled header is not a limit). Behind an L7 load
balancer every tunnel therefore counts against the balancer's address — leave
the per-IP cap at 0 there and use the global one.

## Restarts and decommissions

**Gateway or node restart.** `http.Server.Shutdown` neither waits for nor closes
hijacked connections, so a graceful shutdown would otherwise return immediately
and the process would exit with tunnels still attached. Instead both processes:
stop accepting new handshakes (`503`), close their listeners, wait up to the
drain timeout for live tunnels to end, then force-close the rest.

**Instance decommission.** Draining a route only stops *new* connections;
established ones stay pinned to the instance that accepted them. The teardown
pipeline is therefore:

```
gateway_drain      → re-point the route at the surviving instances
gateway_ws_drain   → poll the gateway until this instance has no tunnels left
                     (bounded by gateway.ws_drain_timeout)
compose_down       → remove the containers
```

The `gateway_ws_drain` step only appears when the instance has a WebSocket
route, and it is a bounded wait, not a guarantee — a decommission that blocks
forever on one stubborn client is worse than a few cut connections.

The gateway cannot send a `1001 Going Away` close frame on the app's behalf (it
never speaks frames). Your app should close its own connections cleanly when it
receives `SIGTERM`.

## Observability

WebSocket gets its own metrics rather than riding on the HTTP ones — a tunnel's
lifetime would land in the `+Inf` bucket of the HTTP latency histogram and
destroy that route's percentiles, and an HTTP metric is only observable once the
request ends, which for a tunnel could be hours.

```
gateway_websocket_handshakes_total{route_key,service_name,env,backend,result}
gateway_websocket_connections_active{route_key,service_name,env,backend}
gateway_websocket_connection_duration_seconds{route_key,service_name,env}
gateway_websocket_disconnects_total{route_key,service_name,env,reason}
```

`result` is one of `success`, `not_enabled`, `unsupported_protocol`,
`origin_rejected`, `rate_limited`, `backend_refused`, `draining`,
`total_limit`, `route_limit`, `client_limit`. `reason` is `peer_closed`,
`idle_timeout` or `drain`.

Handshakes are counted the moment they succeed (also as `1xx` in
`gateway_requests_total`), and the gateway logs `websocket tunnel opened` /
`websocket tunnel closed` with the trace id, so a connection is visible in logs
and dashboards while it is still open.

`GET /-/ws/connections` (service token with `route.read`) lists live tunnels per
route and instance; `?route_key=` and `?instance=` narrow it. This is what the
decommission wait polls.

## Writing the app

**Authenticate during the handshake.** Browsers cannot set `Authorization` or
any other custom header on a WebSocket, so the usual options are a cookie, a
subprotocol value, or a short-lived ticket in the query string. If you use a
ticket, don't log the full query string.

**Set the Origin allowlist** for any cookie-authenticated browser WebSocket.
Cookies are attached cross-origin on WebSocket handshakes, so without it another
site can open connections as your logged-in users — this is the WebSocket
equivalent of CSRF, and the allowlist is the defence.

**Don't rely on instance pinning for stickiness.** The `X-Agenda-Instance`
header can't be set by a browser WebSocket client either. Multi-instance
services should share state through Redis Pub/Sub, Kafka, or similar rather than
an in-memory connection table on one instance.

**Send Pings** (every 20–30s is typical) if you disable the idle timeout, and
reconnect with exponential backoff *plus jitter* — a deploy that drops N
connections at once will otherwise produce a synchronized reconnect storm.

## Not supported

- HTTP/2 Extended CONNECT (RFC 8441) — HTTP/1.1 handshake only.
- Any Upgrade protocol other than `websocket`.
- Frame-level inspection, rewriting, or fan-out. The gateway is a byte tunnel by
  design; per-message routing belongs in your app.
