# CI

The default GitHub Actions workflow runs:

```bash
make check
make test
make scan-artifacts
```

`make check` is a no-Docker static/synthetic test set. `make test` is the full
local runtime verification and uses Docker Compose.

PostgreSQL source-tree checks are intentionally opt-in. Use the
`source-check` workflow manually, or run locally:

```bash
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=run make workload-run WORKLOAD_SPEC=pg-source/check
```

The manual workflow defaults to `PG_SOURCE_ACTION=plan` so a heavy source build
is never part of the default push or pull-request path.

Native `pg_upgrade` is also opt-in. The workload defaults to a dry plan:

```bash
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

Set `PG_UPGRADE_ACTION=check` or `run` only with a `PG_UPGRADE_IMAGE` that
contains the required old and new PostgreSQL binary directories.
