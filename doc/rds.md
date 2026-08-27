# Databases: reading MySQL and Redis from the console

The Databases section lets an operator register a database and read it from the
web console — read-only SQL against MySQL, read-only commands against Redis —
without opening a database port to the network and without anyone SSH-ing into
the machine.

Everything below applies to both engines unless a section says otherwise; the
Redis specifics are gathered under [Redis](#redis).

## How a query travels

```
browser  ──JWT──▶  control plane  ──node token──▶  agenda-node  ──▶  127.0.0.1:3306
                                                                └──▶  127.0.0.1:6379
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
resident node to relay through. Both consoles run over this one path: the SQL
relay is `POST /v1/db/query` on the node, the Redis relay `POST /v1/redis/command`,
and each re-validates what it was handed rather than trusting the control plane
to have done it.

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

### The database must listen on the bridge as well as loopback

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

Redis has exactly the same two problems and the same two fixes: `bind 127.0.0.1
172.17.0.1` in `redis.conf`, or `172.17.0.1:6379:6379` if it is containerized.
Its equivalent of the account's host scoping is the ACL rule
`ACL SETUSER agenda_ro ... ` plus, if you want it, a `bind` narrow enough that
nothing else can reach the port at all.

## The read-only MySQL account

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
password). You will need the engine, the machine, the port, the account, and
optionally a default — a schema for MySQL, a numeric DB index for Redis.
Choosing the engine fills in its conventional port (3306 / 6379).

The password is encrypted at rest with `security.master_key`, exactly like a
machine's agent token. Without a master key configured it is stored in plaintext
and the control plane warns on every write.

Use **Test** to confirm the node can reach the database with those credentials —
it reports the server version on success and the connection error on failure.
For Redis the test is a `PING`, so an ACL narrow enough to withhold `INFO` still
passes; the version is then simply omitted.

## Who can query what

The environment on an instance decides who may run statements or commands
against it, whichever engine it is:

| Environment | Who can query |
|---|---|
| `prod`, `stage` | admins only |
| `test` | any signed-in user |

Registering, editing and removing an instance is always admin-only.

This is deliberately coarse. The whole rule is one function —
`service.AuthorizeQuery` — so replacing it with a per-instance ACL later does
not touch the handlers or the query path.

## What SQL may be run

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

## The SQL editor

The SQL console's editor does MySQL syntax highlighting and completion for keywords,
tables and columns, using the schema you have selected. `Cmd`/`Ctrl`+`Enter`
runs the statement.

Columns complete both qualified and bare, so all three of these work:

```sql
SELECT * FROM orders WHERE cus…       -- columns of the tables in the statement
SELECT * FROM orders WHERE orders.…   -- qualified by table name
SELECT * FROM orders o WHERE o.…      -- qualified by alias
```

With more than one table in the statement, each suggestion is labelled with the
table or alias it came from.

The schema picker groups MySQL's own schemas (`information_schema`,
`performance_schema`, `mysql`, `sys`) under **System**, below your own. They are
grouped rather than hidden: querying `information_schema` from a console is a
reasonable thing to want, it just should not sit between you and your own
schemas.

## Redis

A Redis registration works like a MySQL one — same machine binding, same
password-at-rest, same environment rule, same audit trail. Three things differ.

### The account

The boundary is the same kind of thing it is for MySQL: what the account may do.
On Redis 6+ that is an ACL user.

```
ACL SETUSER agenda_ro on >REPLACE_WITH_A_STRONG_PASSWORD ~* \
  +@read +@connection -@admin -@dangerous
```

`+@read` is the read category and `+@connection` covers `PING` and `AUTH`;
nothing in `@write` or `@scripting` is granted at all. The two `-` rules are
belt and braces — a handful of commands sit in more than one category, and
subtracting the administrative ones after adding the others removes any that
came along.

So a command the console's guard somehow let through is still refused by the
server, which is the point: the guard catches mistakes, the ACL is the
boundary.

Two useful extras, both optional:

```
# Let the console show the server version and read config values.
ACL SETUSER agenda_ro +info +config|get

# Restrict what the account can see at all, by key prefix.
ACL SETUSER agenda_ro resetkeys ~cache:* ~session:*
```

`ACL GETUSER agenda_ro` shows what ended up applied. Check it the way the MySQL
section suggests — connect as the account and confirm a write is refused:

```
SET probe 1      # (error) NOPERM ... has no permissions to run the 'set' command
```

An older Redis with no ACL support has only `requirepass`, which authenticates
but authorizes nothing. There the guard is all that stands between the console
and a write, and this module should be pointed at a replica rather than a
primary.

The username is optional in the registration form: blank means Redis's `default`
user, which is what a `requirepass`-only server has. The password may be blank
too — a Redis bound to loopback often has no `requirepass` at all, and demanding
one would only produce a password the server never checks.

### What may be run

One command per run, from an allowlist of read-only commands
(`internal/redisguard`). Refusing anything not listed means a command introduced
by a future Redis version is refused until someone has looked at it, which is
the safe direction for a default.

Worth knowing about the refusals that are not obvious:

| Refused | Why |
|---|---|
| `GETDEL`, `GETEX`, `PFCOUNT` | They read, and they also write. `PFCOUNT` rewrites the HyperLogLog's cached cardinality. |
| `SORT`, `GEORADIUS` | Both take a `STORE` option. `SORT_RO` is allowed in place of `SORT`. |
| `EVAL`, `EVALSHA`, `FUNCTION` | A script's contents are opaque to any allowlist. |
| `SUBSCRIBE`, `MONITOR`, `BLPOP`, `XREAD` | They block or stream; neither fits a request/response console, and both outlive the statement timeout. |
| `SELECT`, `SWAPDB`, `MOVE` | The DB index is chosen in the console and written to the audit trail. A command must not be able to move itself elsewhere. |
| `CONFIG SET`, `CLIENT KILL`, `DEBUG`, `FLUSHDB` | Administration. |

`KEYS` **is** allowed, and on a large keyspace it will block the server while it
runs. Prefer `SCAN` on anything busy.

Arguments are split the way `redis-cli` splits them — on whitespace, with single
or double quotes grouping an argument that contains spaces. The `\xNN` escapes
`redis-cli` accepts are not implemented, so a key whose bytes cannot be typed
cannot be reached from here.

### Choosing a database, and reading the reply

The console's picker offers `db0`..`db<n-1>`, where `n` comes from
`CONFIG GET databases`. An account that may not read that setting is not an
error — the picker falls back to Redis's own default of 16. The instance's
registered "default DB index" is what the picker starts on.

A reply is rendered into the same columns-and-rows grid a SQL result uses, so
the console, the stored snapshot and the history viewer all stay one
implementation:

| Reply | Rendered as |
|---|---|
| A single value (`GET`, `TTL`, `LLEN`) | One row, one `value` column labelled with the Redis type. |
| A null reply (a key that does not exist) | One row holding `NULL` — an answer, not a failure. |
| An array (`LRANGE`, `KEYS`, `SMEMBERS`) | One row per element, numbered. Nested arrays contribute their own elements in order, so `SCAN` reads as the cursor followed by the keys. |
| `HGETALL`, `CONFIG GET`, `XINFO STREAM` | Two columns, `field` and `value`. |

Values that are not valid UTF-8 are base64-encoded and the column says so, the
same rule the SQL grid follows for `BLOB` columns.

The row, byte and timeout caps are the same as for SQL, and are applied while
the reply is being walked rather than afterwards.

## Query history

Every statement or command that runs is recorded: who ran it, against what
(a Redis entry names the index it ran in, `db0`), how long it took, and a capped
copy of what it returned. Failures are recorded too — "who
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

- MySQL and Redis. The `engine` field is what selects between them, and a third
  engine is a new guard plus a new node executor.
- Reads only. There is no plan to add writes through this path.
- One statement or command at a time. There is no transaction, no pipeline, and
  no `MULTI`.
- Redis Cluster is not modelled: the node opens a plain client, so a `MOVED`
  redirection comes back as an error rather than being followed.
- Editor completion reads `information_schema.COLUMNS` for the selected schema.
  On a server with very many tables that call can be slow or hit the row cap, in
  which case completion is partial or absent — the console says so and stays
  usable.
- The database must be on the machine it is registered against (see above).
