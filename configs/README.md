# PostgreSQL Config Profiles

Config profiles are small `postgresql.conf` snippets applied with
`ALTER SYSTEM` against the disposable workbench instance.

Run:

```bash
make pg-config-apply PG_CONFIG=debug-logging
make docker-reset PG_CONFIG=wal-heavy
```

Profiles are intentionally local and disposable. Settings that require restart
are applied, then the PostgreSQL container is restarted.
