# agenda-v2 Go SDK

First-party Go integration library for the agenda-v2 platform. Published as a
standalone module so importing it does **not** pull in the platform's server
dependencies (gin, gorm, redis, …).

```
go get github.com/FredrickUnderwood/agenda-v2/sdk/go
```

## Packages

- `log` — a thin `zap` wrapper for structured logging in the agenda shape
  (`Init`, `L`, and context-aware `Info`/`Warn`/`Error`/`Debug`). Set
  `log.ContextFields` to enrich every line with request/trace ids from your
  context.
- `log/ginlog` — gin middleware that propagates the `X-Agenda-Trace-Id` trace id
  into the request context so `log.*` lines carry `trace_id`.
- `log/clientlog` — server-side ingest for browser logs shipped by the TypeScript
  SDK (`@agenda/log`, see [`../ts`](../ts)). Mount `clientlog.Handler` on a route
  and browser/React client logs land in this backend's own log file (and view-logs),
  carrying the request's `trace_id`. Turns "frontend logs" into first-class
  view-logs entries. See [`../../doc/frontend-logs.md`](../../doc/frontend-logs.md).
- `metric` / `metric/ginmetric` — Prometheus instrumentation in the agenda shape.
- `alert` — self-contained multi-channel alerting (Feishu/DingTalk/WeCom/Slack/custom).

More packages (identity/permission client against the platform's built-in auth)
land as the platform grows.

## Versioning

This module is versioned independently of the platform under the `sdk/go/vX.Y.Z`
tag prefix, so SDK consumers are not forced to track server releases.
