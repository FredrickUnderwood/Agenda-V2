# Getting frontend logs into view-logs

**Problem:** a frontend deployed on agenda (a React SPA served by nginx) shows an
empty **Logs** tab in the console, because nothing it produces lands in the
files agenda's view-logs reads. This doc explains why, and the two-phase way to
fix it.

## Why they're invisible: view-logs reads host files

The whole logging pipeline is file-based:

```
app writes JSON lines ─▶ /var/log/agenda/<app>__<instance>__<service>.log
   (AGENDA_LOG_DIR, bind-mounted by the deploy override to
    <root>/run/<app>/<env>/<instance>/logs on the host)
                              │
agenda-node  GET /v1/logs/:app/:instance   (globs <app>__<instance>*.log, tails the end)
                              │
control-plane ApplicationLogService ─▶ console Logs tab
```

`sdk/go/log` writes exactly those files, which is why Go backends "just work".
A frontend has two log sources, **neither** of which produces them by default:

| | A. The **container** (nginx) | B. The **browser** runtime |
| --- | --- | --- |
| Logs | access / error logs | `console`, uncaught errors, promise rejections, failed fetches, page events |
| Why invisible | nginx writes its own files/stdout, not `/var/log/agenda/<app>__…​.log` | runs in end-users' browsers — never touches the host at all |
| Fix | **Phase 0**: point nginx's log at the mounted dir (no SDK) | **Phase 1**: `@agenda/log` browser SDK → server ingest |

**The hard constraint for B:** a browser cannot write host files, so a browser
SDK can only *ship* logs over HTTP to a server-side sink that writes them. Any
design that skips that sink cannot reach view-logs.

---

## Phase 0 — nginx container logs (no SDK, minutes)

Make nginx emit JSON to the platform-mounted log directory, using the identity
env vars the deploy injects (`AGENDA_APP_NAME`, `AGENDA_INSTANCE_NAME`,
`AGENDA_SERVICE_NAME`, `AGENDA_LOG_DIR`). nginx can't read env vars in its config
directly, so template the access-log path at container start with `envsubst`.

`nginx.conf.template`:

```nginx
log_format agenda_json escape=json
  '{"level":"info","ts":"$time_iso8601","msg":"http_access",'
  '"app":"${AGENDA_APP_NAME}","service":"${AGENDA_SERVICE_NAME}",'
  '"env":"${AGENDA_ENV}","instance":"${AGENDA_INSTANCE_NAME}",'
  '"method":"$request_method","path":"$uri","status":$status,'
  '"bytes":$body_bytes_sent,"rt":$request_time,"ua":"$http_user_agent",'
  '"trace_id":"$http_x_agenda_trace_id"}';

server {
  listen 80;
  access_log ${AGENDA_LOG_DIR}/${AGENDA_APP_NAME}__${AGENDA_INSTANCE_NAME}__${AGENDA_SERVICE_NAME}.log agenda_json;
  error_log  stderr warn;

  location / { try_files $uri /index.html; }   # SPA fallback
}
```

Docker entrypoint (render the template, then exec nginx):

```dockerfile
CMD ["/bin/sh","-c","envsubst '$AGENDA_APP_NAME $AGENDA_INSTANCE_NAME $AGENDA_SERVICE_NAME $AGENDA_ENV $AGENDA_LOG_DIR' \
  < /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf && exec nginx -g 'daemon off;'"]
```

The filename **must** be `<app>__<instance>__<service>.log` — that's the exact
glob `agenda-node` matches (see `internal/node/logs.go`). Now the frontend's
Logs tab shows request-level access logs. This is Docker plumbing, not an SDK —
it complements, and is superseded in value by, Phase 1.

---

## Phase 1 — browser runtime logs (`@agenda/log` + Go ingest)

The valuable frontend logs are client-side: JS exceptions your users actually
hit, failed API calls, page events. Capture them in the browser and POST them to
your **existing backend**, which re-emits them through `sdk/go/log`.

```
React app (browser)                         your Gin backend (already deployed)
  @agenda/log/browser  ──POST /api/client-logs──▶  clientlog.Handler
    capture window.onerror / rejections            re-emit via sdk/go/log
    + explicit logger.info/error                        │
    batch + sendBeacon/fetch                            ▼
                                    /var/log/agenda/<backend>__…​.log ─▶ node ─▶ view-logs
```

**1. Browser** (see [`sdk/ts/README.md`](../sdk/ts/README.md)):

```ts
import { createBrowserLogger } from "@agenda/log/browser";
const { logger } = createBrowserLogger({ endpoint: "/api/client-logs" });
logger.error("payment failed", { code });
```

**2. Same-origin proxy** — the frontend's nginx forwards `/api/client-logs` to
the backend's gateway internal route, so there's no CORS:

```nginx
location /api/ { proxy_pass http://GATEWAY_HOST:8081/svc-myapp-api/; }
```

**3. Backend ingest** — mount once, behind `ginlog.Middleware()` so the ingested
lines carry the request's `trace_id`:

```go
r.Use(ginlog.Middleware())
r.POST("/api/client-logs", gin.WrapH(clientlog.Handler(clientlog.Options{})))
```

### Where the client logs appear

Because the backend is the sink, client logs land under the **backend app's**
Logs tab (not the frontend app's), tagged `source=client` and sharing the
request's `trace_id` — so a browser error and the API calls around it correlate.
That's the deliberate Phase-1 trade: zero new platform surface, full reuse of the
existing Go SDK and trace propagation.

> To instead file client logs under the **frontend** app, a later phase adds a
> server-side TS sink (`@agenda/log` node entry) — either serving the frontend
> from Node (Next.js/Express) or a tiny collector sidecar sharing the frontend
> instance's `/var/log/agenda` mount. Not needed for Phase 1.

### Safety of a browser-facing endpoint

`clientlog.Handler` is public (any browser can POST), so it defends itself: it
caps body size, batch length, message length and serialized field size, folds
unknown levels to `info`, and nests all browser-supplied fields under a single
`client` object so a client can never overwrite a line's identity fields. Add
app-level rate limiting if abuse is a concern. This keeps the platform's
invariant intact — no public write endpoint is added to control-plane or node;
the app's own backend absorbs the traffic.

## Requirements & limits

- Log reading requires an **agent-mode** machine (agenda-node); SSH-mode targets
  can't serve logs. Same limit as backend logs.
- Don't hand-set `AGENDA_*` env or hand-mount `/var/log/agenda` — the deploy
  override injects both. Phase 0 only *reads* the injected values.
