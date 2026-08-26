# Databases: read-only SQL from the console

The Databases section lets an operator register a database and run read-only
SQL against it from the web console, without opening a database port to the
network and without anyone SSH-ing into the machine.

## How a query travels

```
browser  ──JWT──▶  control plane  ──node token──▶  agenda-node  ──▶  127.0.0.1:3306
```

The control plane never opens a database connection. It hands the statement to
the `agenda-node` agent on the database's machine, and the node connects over
its own `proxy_backend_host`. This is the same invariant log tailing, metric
scraping and health probing already follow, and it has two consequences worth
stating plainly:

- **The database port never has to be published.** Bind it to loopback and the
  container bridge; nothing outside the host needs to reach it.
- **The database must live on the machine it is registered against.** There is
  no host field. A managed cloud database reachable only by its own endpoint
  cannot be registered.

Only agent-mode machines can host a registered database — an SSH machine has no
resident node to relay through.

## Before you register anything

Both of the following come from the same fact: `install-node.sh` runs
agenda-node **in a container**, with `proxy_backend_host: host.docker.internal`.
Getting either wrong produces a confusing failure on the very first connection
attempt.

### The node does not connect from 127.0.0.1

A containerized node reaches the host through a Docker bridge, so the connection
arrives at MySQL from a bridge address, not from loopback. An account created as
`'agenda_ro'@'localhost'` is refused with `Access denied`.

**Do not guess which address.** `172.17.0.0/16` is only the stock Linux default.
Docker Desktop, and any daemon with custom `default-address-pools`, hands out
something else — `192.168.x` is common — and an account scoped to the wrong
range fails exactly like a wrong password.

Create the account with the `%` wildcard, connect once, then let MySQL tell you
where the connection came from:

```sql
CREATE USER 'agenda_ro'@'%' IDENTIFIED BY '<a strong random password>';
GRANT SELECT, SHOW VIEW ON `app_db`.* TO 'agenda_ro'@'%';
```

Register the instance, press **Test**, and read the real source address:

```sql
SELECT user, host FROM information_schema.processlist WHERE user = 'agenda_ro';
```

Then narrow the account to that subnet. `RENAME USER` carries the grants across,
so nothing needs re-granting:

```sql
RENAME USER 'agenda_ro'@'%' TO 'agenda_ro'@'192.168.128.%';
```

MySQL's host part takes `%` and `_` as wildcards — there is no `*`, and a host
written as `'*'` matches nothing at all. When you know the subnet, a netmask is
more precise than a wildcard: `'agenda_ro'@'192.168.128.0/255.255.255.0'`.

If you instead run agenda-node bare-metal under systemd
(`cmd/agenda-node/agenda-node.service`), the connection does come from
`127.0.0.1` and `@'localhost'` is correct.

### MySQL must listen on the bridge as well as loopback

With `bind-address = 127.0.0.1`, a containerized node cannot reach MySQL at all.
It also has to listen on the bridge gateway. Find that address rather than
assuming it:

```bash
docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}'
```

MySQL 8.0.13+ accepts a list (substitute the gateway you just found):

```ini
[mysqld]
bind-address = 127.0.0.1,172.17.0.1
```

The external interface is still not listening, so this does not undo the point
of the exercise.

If MySQL itself runs as a container on that machine, the same applies to how its
port is published: `127.0.0.1:3306:3306` is unreachable from the node, while
binding it to the bridge gateway — `172.17.0.1:3306:3306`, again substituting
your own — works.

## The read-only account

agenda parses every statement and sets `transaction_read_only` on the session,
but **neither is the security boundary**. Both are there to catch mistakes. The
boundary is what the account is allowed to do.

### Grant at the database level, not the table level

MySQL checks privileges in tiers — global, database, table, column — and stores
them in different places. That is what decides whether new tables are covered:

- `GRANT SELECT ON db.*` writes a **database-level** row in `mysql.db`. It
  matches any table in that schema, so **tables created later are covered with
  no further grants**.
- `GRANT SELECT ON db.tbl` writes a **table-level** row in `mysql.tables_priv`,
  which matches that one table. This is the form that needs re-granting every
  time someone adds a table.

So grant at the database level:

```sql
-- Host scoping: start with '%' and narrow it once you know the real source
-- address (see "The node does not connect from 127.0.0.1" above).
CREATE USER 'agenda_ro'@'%' IDENTIFIED BY '<a strong random password>';

-- Covers every table in the schema, including ones that do not exist yet.
GRANT SELECT, SHOW VIEW ON `app_db`.* TO 'agenda_ro'@'%';

-- One bad query should not be able to exhaust the connection pool.
ALTER USER 'agenda_ro'@'%'
  WITH MAX_USER_CONNECTIONS 5
       MAX_QUERIES_PER_HOUR 20000;
```

`SHOW VIEW` is only needed if you want view definitions and schema browsing;
plain `SELECT` is enough to run queries.

To cover every schema, including ones created later, grant globally:

```sql
GRANT SELECT, SHOW VIEW ON *.* TO 'agenda_ro'@'%';
```

That also exposes `mysql` (where the password hashes live) and `sys`. On MySQL
8.0.16+ you can carve those back out:

```sql
SET PERSIST partial_revokes = ON;
REVOKE SELECT ON `mysql`.*              FROM 'agenda_ro'@'%';
REVOKE SELECT ON `sys`.*                FROM 'agenda_ro'@'%';
REVOKE SELECT ON `performance_schema`.* FROM 'agenda_ro'@'%';
```

`partial_revokes` is a server-wide switch that cannot be turned off again while
partial revokes exist, so prefer per-schema grants unless you really need the
global form.

### What not to grant

| Privilege | Why not |
|---|---|
| `FILE` | `SELECT ... INTO OUTFILE` writes a file on the database server. Withholding it is the server-side backstop for the statement guard. |
| `PROCESS` | `SHOW PROCESSLIST` exposes statements running in other sessions. |
| `SUPER`, `SYSTEM_VARIABLES_ADMIN` | Can change global variables, including turning `transaction_read_only` back off. |
| `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP`, `ALTER` | Directly contradicts the point of the account. |

### Check it

```sql
SHOW GRANTS FOR 'agenda_ro'@'%';
```

Expect only `GRANT USAGE ON *.*` and the schema grant. Then connect as that user
and confirm:

```sql
SELECT 1;                        -- succeeds
CREATE TABLE t_probe(id int);    -- ERROR 1142, command denied
SELECT 1 INTO OUTFILE '/tmp/x';  -- ERROR 1045, access denied (no FILE)
```

The third one is worth actually running: it is what proves the backstop is in
place rather than assumed.

## Registering a database

**Databases → Instances → Register database** (admins only, because it stores a
password). You will need the machine, the port, the account, and optionally a
default schema.

The password is encrypted at rest with `security.master_key`, exactly like a
machine's agent token. Without a master key configured it is stored in plaintext
and the control plane warns on every write.

Use **Test** to confirm the node can reach the database with those credentials —
it reports the server version on success and the connection error on failure.

## Who can query what

The environment on an instance decides who may run statements against it:

| Environment | Who can query |
|---|---|
| `prod`, `stage` | admins only |
| `test` | any signed-in user |

Registering, editing and removing an instance is always admin-only.

This is deliberately coarse. The whole rule is one function —
`service.AuthorizeQuery` — so replacing it with a per-instance ACL later does
not touch the handlers or the query path.

## What may be run

Single statements only, beginning with `SELECT`, `WITH`, `SHOW`, `DESCRIBE`,
`DESC` or `EXPLAIN`. Also refused: `INTO OUTFILE` / `INTO DUMPFILE`, MySQL
executable comments (`/*! ... */`, which the server runs), and anything with a
second statement after a semicolon. The driver is configured without
multi-statement support, so a semicolon could not start a second statement even
if it got through.

Comments and a trailing semicolon are stripped before execution, so what was
validated is exactly what runs.

Limits, all applied while the result is being read rather than afterwards:

| Limit | Default | Ceiling |
|---|---|---|
| Rows | 1000 | 10000 |
| Result size | 8 MB | 32 MB |
| Statement timeout | 15 s | 60 s |

The node also asks the server to enforce the timeout itself
(`max_execution_time`, or `max_statement_time` on MariaDB), so a query outlives
neither side.

## Query history

Every statement that runs is recorded: who ran it, against what, how long it
took, and a capped copy of what it returned. Failures are recorded too — "who
tried to run what" is the question an audit trail exists to answer.

A signed-in user sees their own history; an admin sees everyone's.

Two things to know about the stored results:

- They are **real production data living in the control-plane database**, and in
  its backups. They are encrypted with `security.master_key`, and removed once
  they pass the retention window — `rds.query_log_retention_days` in Settings,
  30 days by default. Set it to match your own data-handling policy.
- A large result is kept in outline only (the first 200 rows, up to 256 KB). The
  history entry says so; re-run the query to see the rest.

A statement that was **refused** — by the read-only guard or by the permission
check — is not recorded, because it never ran. It is reported in the control
plane's own logs.

## Limits of this version

- MySQL only. The `engine` field exists so another can be added, but nothing
  else is implemented.
- Reads only. There is no plan to add writes through this path.
- Schema browsing covers schemas and tables, not columns.
- The database must be on the machine it is registered against (see above).
