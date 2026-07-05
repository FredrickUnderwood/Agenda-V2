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

More packages (identity/permission client against the platform's built-in auth)
land as the platform grows.

## Versioning

This module is versioned independently of the platform under the `sdk/go/vX.Y.Z`
tag prefix, so SDK consumers are not forced to track server releases.
