# Security Policy

## Supported versions

agenda-v2 is pre-1.0 and under active development. Security fixes land on the
default branch (`master`); please run the latest commit.

| Version | Supported |
|---|---|
| `master` (latest) | ✅ |
| older commits / tags | ❌ |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately through GitHub's **[Private Vulnerability Reporting](https://github.com/FredrickUnderwood/Agenda-V2/security/advisories/new)**
(the repository's *Security → Advisories → Report a vulnerability*). This keeps the
report confidential until a fix is available and lets us coordinate a disclosure.

When reporting, please include:

- affected component (`control-plane` / `gateway` / `node` / `sdk` / `web`) and
  version/commit,
- a description of the issue and its impact,
- reproduction steps or a proof of concept, and
- any suggested remediation, if you have one.

## What to expect

- **Acknowledgement**: we aim to acknowledge a report within a few days.
- **Assessment**: we will confirm the issue, determine severity, and keep you
  updated on remediation progress.
- **Fix & disclosure**: once a fix is ready we will release it and, with your
  consent, credit you in the advisory. Please give us a reasonable window to
  remediate before any public disclosure.

## Hardening the Databases module

The Databases module lets an operator read a registered MySQL or Redis from the
web console — read-only SQL, or read-only Redis commands. Neither travels over a
database port exposed to the network: the control plane relays them to the
machine's `agenda-node` agent, which opens the connection locally. Three
properties are yours to configure, and the module is only as safe as the weakest
of them.

**1. Use a dedicated read-only database account.** agenda-v2 parses each
statement and rejects anything that is not a single
`SELECT`/`WITH`/`SHOW`/`DESCRIBE`/`EXPLAIN`, and it sets
`transaction_read_only` on every session. Treat both as protection against
mistakes, not as a security boundary — the boundary is the account's own grants.
Grant `SELECT` (and `SHOW VIEW` if you want schema browsing) at the *database*
level so newly created tables are covered without re-granting, and do not grant
`FILE`, `PROCESS`, or `SUPER`. See [doc/rds.md](doc/rds.md) for the exact
statements.

The same reasoning applies to Redis, where the boundary is an ACL user rather
than a grant: `ACL SETUSER agenda_ro on >... ~* +@read +@connection -@admin
-@dangerous`. The command allowlist agenda-v2 enforces is again there to catch
mistakes, not to contain a hostile caller. A server old enough to have only
`requirepass` authenticates but authorizes nothing, so point the module at a
replica rather than a primary.

**2. Keep the database bound to loopback and the container bridge only.** The
node agent connects to `<proxy_backend_host>:<port>` on its own machine. Nothing
needs to reach the database port from outside the host — do not publish it, and
that goes for Redis (`bind` in `redis.conf`) as much as for MySQL. A
containerized node connects from a bridge address rather than loopback; find
which one rather than assuming it, and scope the account to that subnet.

**3. Set `security.master_key`.** Database passwords and stored results are
encrypted at rest with this key. Without it they are written to the
control-plane database in plaintext, and the control plane logs a warning on
every write.

Two further notes on the audit trail:

- Query results are stored (capped and encrypted) so users can revisit their own
  history, which means production data reaches the control-plane database and its
  backups. Tune `rds.query_log_retention_days` to your data-handling policy.
- The `agenda-node` management port carries database credentials on every query
  and every command.
  It must be bound to a private interface or a VPN; if it has to cross an
  untrusted network, terminate TLS in front of it.

## Scope notes

agenda-v2 is infrastructure you self-host, so its security also depends on how you
deploy it. A few things that are **your responsibility as the operator** rather
than platform vulnerabilities:

- Keeping the generated secrets in `deploy/quickstart/.env` and your
  `config/agenda-v2.yaml` out of version control (they are git-ignored by default).
- Restricting network access to the control plane, the `agenda-node` management
  port, and the database to trusted networks.
- Setting `security.master_key` and `auth.jwt_secret` to strong random values and
  rotating the bootstrap admin credentials after first login.

Reports about missing hardening in these operator-controlled areas are welcome as
regular issues or discussions rather than private security reports.
