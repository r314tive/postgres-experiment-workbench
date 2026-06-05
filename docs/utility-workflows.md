# Utility Workflows

Utility workflows combine profiles, topology specs, workload specs, metrics,
snapshots, scans, and experiment verdicts. Use them to test external tools,
PostgreSQL utilities, PostgreSQL source trees, and data behavior under
controlled local pressure.

Prefer `make experiment-run` when the workflow should leave a self-contained
run directory under `runs/<run-id>/`. Use `make workload-run` for direct smoke
checks while iterating on a specific tool adapter.

## Dump And Restore

Run `pg_dump` while WAL pressure is active:

```bash
make experiment-plan EXPERIMENT_SPEC=pgdump-under-wal-pressure
make experiment-run EXPERIMENT_SPEC=pgdump-under-wal-pressure
```

Run direct utility workloads against the current local database:

```bash
make profile-reset PROFILE=smoke PROFILE_SIZE=small
make workload-run WORKLOAD_SPEC=utility/pg-dump-smoke
make workload-run WORKLOAD_SPEC=utility/pg-restore-smoke
make workload-run WORKLOAD_SPEC=utility/pg-dumpall
```

The dump and restore smoke workloads write local evidence under:

```text
logs/utility/
```

## PgBouncer

Run the pooler smoke experiment:

```bash
make experiment-plan EXPERIMENT_SPEC=pgbouncer-smoke
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
```

Run connection-pressure checks through PgBouncer:

```bash
make experiment-plan EXPERIMENT_SPEC=pgbouncer-connection-pressure
make experiment-run EXPERIMENT_SPEC=pgbouncer-connection-pressure
```

Inspect PgBouncer admin state directly:

```bash
make topology-up TOPOLOGY=pgbouncer
make workload-run WORKLOAD_SPEC=topology/pgbouncer-admin
make workload-run WORKLOAD_SPEC=topology/pgbouncer-prepared-statement
```

## PostgreSQL Source Check Plan

Render and run the lightweight source-check plan:

```bash
make experiment-plan EXPERIMENT_SPEC=pg-source-plan
make experiment-run EXPERIMENT_SPEC=pg-source-plan
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/check
```

Real PostgreSQL source builds are opt-in. Keep them outside default CI unless a
specific run needs them:

```bash
PG_SOURCE_ACTION=run make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=run PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/chaos-check
```

Patchsets are cataloged under `patchsets/`:

```bash
make patchset-list
make patchset-show PATCHSET=chaos/master
make patchset-validate
```

Source-check artifacts remain local and ignored. Scan them with the generic
failure scanner and classify the source-check artifact shape before trusting a
run:

```bash
make source-classify SOURCE_CHECK_PATH=generated/pg-source/<run-id>
make scan-artifacts
make scan-artifacts-go
```

## Upgrade Path

Run the dump/restore upgrade smoke path:

```bash
make experiment-plan EXPERIMENT_SPEC=multi-version-upgrade-smoke
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
```

Run upgrade topology workloads directly:

```bash
make topology-up TOPOLOGY=multi-version-upgrade
make workload-run WORKLOAD_SPEC=topology/upgrade-status
make workload-run WORKLOAD_SPEC=topology/upgrade-dump-restore
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

`topology/native-pg-upgrade` defaults to `PG_UPGRADE_ACTION=plan`. Native
`check` or `run` modes require an image containing both PostgreSQL versions and
matching bindir variables:

```bash
PG_UPGRADE_ACTION=check \
PG_UPGRADE_IMAGE=your/pg-upgrade-image:tag \
PG_UPGRADE_OLD_BINDIR=/path/to/old/bin \
PG_UPGRADE_NEW_BINDIR=/path/to/new/bin \
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

## Review Evidence

Every experiment run should be reviewable without re-running it:

```bash
make experiment-report RUN_DIR=runs/<run-id>
make experiment-verify RUN_DIR=runs/<run-id>
make scan-artifacts
make scan-artifacts-go
make diagnostics-run DIAGNOSTIC=activity
make diagnostics-run DIAGNOSTIC=locks
```

For repeated or matrix runs:

```bash
make experiment-summary SUMMARY_INPUT=runs/repeats/<repeat-id>
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
make matrix-plan MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
```
