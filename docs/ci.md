# CI

The default GitHub Actions workflow runs:

```bash
make check
make test
make scan-artifacts
```

`make check` is a no-Docker static/synthetic test set, including Go unit tests,
Go profile validation, Go env spec validation, Go run artifact verification,
Go env spec reference/schema rendering, Go experiment plan rendering, Go
patchset validation, Go source-check plan rendering, Go source-check artifact
classification tests, and Go failure scanning.
`make test` is the full local runtime verification and uses Docker Compose.
`make release-check` is the local pre-release gate: it runs doctor checks,
static checks, quickstart, full runtime tests, artifact scans, privacy scan, and
the local `pgworkbench` build.

PostgreSQL source-tree checks are intentionally opt-in. Use the
`source-check` workflow manually, or run locally:

```bash
make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_PATCHSET=chaos/master make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/check
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

## Release Snapshot Workflow

The `release-snapshot` workflow builds ignored local-style `pgworkbench`
archives and uploads them as workflow artifacts. It runs on tags matching `v*`
and can also be started manually:

```bash
gh workflow run release-snapshot.yml -f version=0.1.0
```

The workflow runs `make check` before building archives with:

```bash
make release-snapshot VERSION=<version>
```

On tag pushes, the workflow also creates or updates the matching GitHub Release
and attaches the generated archives.
