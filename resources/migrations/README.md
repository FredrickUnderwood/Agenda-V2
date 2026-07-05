# Migrations

Schema is managed exclusively through the `.sql` files in this directory —
there is no `AutoMigrate` anywhere in this codebase (see
`internal/repository/db.go`). Apply them by hand, in filename order, against
the target MySQL instance before starting the server:

```
mysql -h <host> -u <user> -p <database> < resources/migrations/0001_init_schema.sql
```

When a future change needs a schema change, add a new numbered file
(`0002_xxx.sql`) rather than editing `0001_init_schema.sql` in place, and
apply it the same way against every environment (dev/stage/prod) before
deploying the corresponding code.
