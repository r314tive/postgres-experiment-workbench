# Utility Workflows

Utility workflows combine profiles, topology specs, workload specs, metrics,
snapshots, scans, and experiment verdicts. Use them to test external tools,
PostgreSQL utilities, PostgreSQL source trees, and data behavior under
controlled local pressure.

Prefer `make experiment-run` when the workflow should leave a self-contained
run directory under `runs/<run-id>/`. Use `make workload-run` for direct smoke
checks while iterating on a specific tool adapter.

Use utility-test specs when the same tool scenario needs a named, reviewable
plan before execution. These specs live under `utility-tests/**/*.env` and
point at ordinary workload specs for the foreground utility action:

```bash
make utility-list
make utility-show UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-json UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-expanded UTILITY_TEST_SPEC=pg-dumpall/wal-pressure
go run ./cmd/pgworkbench utility validate
```

The utility plan contract covers profile setup, dataset load, background
workloads, metrics sampling, foreground utility workload, expected output files,
SQL assertions, shell assertions, extra failure-scan paths, and evidence. It is
intentionally generic; `pg_dump`, `pg_restore`, external backup tools, data
checkers, fuzzers, and PostgreSQL source utilities should all fit the same
shape.

Run a utility-test through the existing experiment runner when you want a full
`runs/<run-id>/` artifact:

```bash
make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
make utility-run-json UTILITY_TEST_SPEC=pg-dump/smoke
UTILITY_TEST_RUN_ID=manual-pgdump make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
```

`utility run` generates an ignored temporary experiment spec under `.tmp/` and
then delegates to `scripts/run_experiment.sh`. That keeps utility tests generic
while preserving the same snapshots, metrics, scan, manifest, verdict, report,
and bundle workflow as experiments.

Declare result checks directly in the utility-test spec:

```bash
UTILITY_TEST_EXPECT_FILES="logs/utility/pg-dump-smoke.sql"
UTILITY_TEST_ASSERT_SQL="SELECT count(*) > 0 FROM restore_check.items;"
UTILITY_TEST_ASSERT_SQL_FILES="sql/assertions/after_restore.sql"
UTILITY_TEST_ASSERT_SHELL='test -s "$REPO_DIR/logs/utility/custom.log"'
UTILITY_TEST_SCAN_PATHS="logs/utility generated/tool-output"
```

Expected files are converted into non-empty file assertions. SQL and shell
assertions are passed through to the experiment runner.

## Dump And Restore

Preview native PostgreSQL utility scenarios:

```bash
make utility-plan UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan UTILITY_TEST_SPEC=pg-restore/smoke
make utility-plan UTILITY_TEST_SPEC=pg-dumpall/smoke
make utility-plan-expanded UTILITY_TEST_SPEC=pg-dumpall/wal-pressure
make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
```

Run `pg_dump` while WAL pressure is active:

```bash
make experiment-plan EXPERIMENT_SPEC=pgdump-under-wal-pressure
make experiment-run EXPERIMENT_SPEC=pgdump-under-wal-pressure
```

Run direct utility workloads against the current local database:

```bash
make profile-reset PROFILE=smoke PROFILE_SIZE=small
make workload-run WORKLOAD_SPEC=utility/pg-dump-smoke
make workload-run-json WORKLOAD_SPEC=utility/pg-dump-smoke
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
