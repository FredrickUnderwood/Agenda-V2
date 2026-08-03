# @agenda/log — agenda-v2 TypeScript logging SDK

First-party client-side logging for apps hosted on **agenda-v2**. It gets your
**frontend** logs into the platform's **view-logs** — the same console Logs tab
that `sdk/go/log` feeds for backends.

Published as a single package with three layered entry points (dependency-free
core, so importing `@agenda/log/core` pulls in no DOM or React):

| Entry | Runs in | What it is |
| --- | --- | --- |
| `@agenda/log` / `@agenda/log/core` | anywhere | the log shape, levels, and the wire contract (isomorphic, zero-dep) |
| `@agenda/log/browser` | browser | captures + batches + ships client logs to an ingest endpoint |
| `@agenda/log/react` | browser + React | `<AgendaLogProvider>`, `useLogger()`, `<AgendaErrorBoundary>` |

> React is an **optional peer dependency** — only `@agenda/log/react` needs it.

## The one constraint that shapes everything

agenda's view-logs reads **files on the host** that `agenda-node` tails
(`/var/log/agenda/<app>__<instance>__<service>.log`, JSON lines). **A browser
can't write those files.** So the browser SDK does not write logs — it batches
and **POSTs** them to a server endpoint that re-emits them through a real
file sink. In Phase 1 that endpoint is your **existing backend**, using
`sdk/go/log/clientlog` (Go). Result: client logs land in view-logs under the
**backend** app, correlated to the request that carried them (shared `trace_id`).

```
React app (browser) ──POST /api/client-logs──▶ your Gin backend
   @agenda/log/browser                            clientlog.Handler
        │ batch + sendBeacon/fetch                      │ re-emit via sdk/go/log
        ▼                                               ▼
   (no files here)                     /var/log/agenda/<backend>__…​.log ─▶ agenda-node ─▶ view-logs
```

## Install

```bash
npm install @agenda/log
# React bindings additionally need react as a peer dep (you already have it)
```

## Browser usage

```ts
import { createBrowserLogger } from "@agenda/log/browser";

const { logger } = createBrowserLogger({
  endpoint: "/api/client-logs",           // same-origin: nginx proxies it to the backend
  level: "info",
  baseFields: { app: "myapp-web", release: import.meta.env.VITE_GIT_SHA },
  captureGlobalErrors: true,               // window.onerror        (default true)
  captureUnhandledRejections: true,        // unhandledrejection    (default true)
  captureConsole: ["error"],               // mirror console.error  (default off)
  getTraceId: () =>                        // correlate to backend request chain
    document.querySelector('meta[name="trace-id"]')?.getAttribute("content") ?? undefined,
});

logger.info("checkout started", { cartId });
logger.error("payment failed", { code });
try { risky(); } catch (e) { logger.captureError(e, { where: "checkout" }); }
```

`createBrowserLogger` installs the global captures, a periodic flush, and a
final flush on page hide (via `navigator.sendBeacon`). It's SSR-safe: outside a
browser `install()` is a no-op. Call the returned `uninstall()` to tear down.

Transport is fire-and-forget and **bounded**: entries batch (`batchSize`, default
20), flush on a timer (`flushIntervalMs`, default 5000 ms) or on page hide, and
the queue is capped (`maxQueue`, default 1000 — oldest dropped) so a dead
endpoint or a burst can never grow memory unbounded.

## React usage

```tsx
import { createBrowserLogger } from "@agenda/log/browser";
import { AgendaLogProvider, AgendaErrorBoundary, useLogger } from "@agenda/log/react";

const { logger } = createBrowserLogger({ endpoint: "/api/client-logs" });

function App() {
  return (
    <AgendaLogProvider logger={logger}>
      <AgendaErrorBoundary fallback={(err) => <Crashed error={err} />}>
        <Routes />
      </AgendaErrorBoundary>
    </AgendaLogProvider>
  );
}

function Checkout() {
  const log = useLogger();
  return <button onClick={() => log.info("pay clicked")}>Pay</button>;
}
```

`AgendaErrorBoundary` reports render/lifecycle crashes — the ones
`window.onerror` never sees — with the React **component stack**.

## Backend ingest (Go)

Mount the ingest handler once, behind `ginlog.Middleware()` so the request's
`trace_id` is attached to the ingested lines:

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/FredrickUnderwood/agenda-v2/sdk/go/log/ginlog"
    "github.com/FredrickUnderwood/agenda-v2/sdk/go/log/clientlog"
)

r.Use(ginlog.Middleware())
r.POST("/api/client-logs", gin.WrapH(clientlog.Handler(clientlog.Options{})))
```

`clientlog.Handler` is a plain `http.Handler` (no gin dependency), so a
`net/http` mux works too. It caps body size, batch length, message length and
serialized field size, and nests all browser-supplied fields under a single
`client` object so they can never overwrite a line's identity fields. See
[`../go/log/clientlog`](../go/log/clientlog).

## Wire contract

`POST <endpoint>` with `Content-Type: application/json`:

```json
{ "logs": [ { "level": "error", "msg": "…", "ts": "2026-07-30T00:00:00.000Z",
              "logger": "window.onerror", "fields": { "url": "…", "stack": "…" } } ] }
```

`level` ∈ `debug|info|warn|error` (aliases folded; unknown → `info`). Everything
but `msg`/`level` is optional. The endpoint answers `204` on success.

## Develop

```bash
npm install
npm run typecheck   # tsc, src
npm test            # tsc + node --test (core + browser transport)
npm run build       # emit dist/ (ESM + .d.ts)
```

## Why one package, not three

The three logical layers (`core` / `browser` / `react`) that were scoped ship as
one package with subpath exports rather than three separately-published npm
packages — lighter to version and consume for a self-hosted platform, with tree
shaking (`"sideEffects": false`) keeping only what you import. It can be split
into `@agenda/log-core` / `-browser` / `-react` later without changing consumer
imports if independent publishing is ever needed.
