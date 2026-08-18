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
PGWORKBENCH_RUNTIME=native UTILITY_TEST_RUN_ID=manual-pgdump make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
go run ./cmd/pgworkbench utility run --runtime native --run-id manual-pgdump pg-dump/smoke
```

`utility run` generates an ignored temporary experiment spec under `.tmp/` and
then delegates to `scripts/run_experiment.sh`. That keeps utility tests generic
while preserving the same snapshots, metrics, scan, manifest, verdict, report,
and bundle workflow as experiments. The adapter path is constrained to
`.tmp/utility-tests/`; symlinked source or generated paths are rejected. The
delegation carries an internal `utility-derived` capability together with the
portable source utility-test ID/reference and its exact SHA-256 digest. The run
manifest records both the generated experiment spec identity and this source
tuple, so a bundle remains attributable to the reviewed `utility-tests/**/*.env`
input after relocation. These internal `PGWORKBENCH_*` values are set by the Go
runner and are not a supported mechanism for admitting arbitrary experiment
paths.

The prepared runner uses an exact workbench/protocol environment, not a complete
OS environment capture. The CLI explicitly projects the selected runtime and
`COMPOSE`, fixes `ENV_FILE=.env.example`, supplies the native
`PGWORKBENCH_NATIVE_BINDIR` or documented `PG_INSTALL_DIR` fallback and wait
budget, the primary `POSTGRES_HOST`/`POSTGRES_PORT`/database/user/password tuple,
and utility profile/metrics/snapshot controls. In exact mode the host shell
resolves that env-file path only so Docker Compose can receive `--env-file`; it
never sources `.env.example`, a checkout-local `.env`, or a caller-selected
`ENV_FILE`. Trusted experiment, workload, and dataset specs cannot change the
runner-owned exact-mode marker.

Ambient experiment hooks, workload commands, benchmark/capsule capabilities,
and a claimed native-toolchain digest are not inherited. Conventional
process-bootstrap names (`HOME`, `LOGNAME`, `PATH`, `TEMP`, `TMP`, `TMPDIR`, and
`USER`) remain inherited, with locale/timezone and `BASH_ENV` fixed by the
runner. They are not evidence identity or host attestation: in particular,
`PATH` can still select the shell and ordinary host helper commands. Native
utility execution does not use `PATH` as its PostgreSQL-tool fallback; it
requires the explicit bindir or installation directory above. The existing
experiment target guard still requires a disposable loopback, non-system
database, while the native backend still validates the canonical local port and
identifiers. Engine version, commit, and executable identity are supplied
directly by the CLI rather than recovered from ambient protocol variables.

Before a passed verdict is written, the generated spec, reviewed source
utility-test spec, and every declared `UTILITY_TEST_EXPECT_FILES` output are
copied into `runs/<run-id>/artifacts/`. They therefore enter the complete
run-bundle inventory; later overwrites under `logs/utility/` do not change the
bundled evidence.

Declare result checks directly in the utility-test spec:

```bash
UTILITY_TEST_TRUSTED_SHELL=1
UTILITY_TEST_EXPECT_FILES="logs/utility/pg-dump-smoke.sql"
UTILITY_TEST_ASSERT_SQL="SELECT count(*) > 0 FROM restore_check.items;"
UTILITY_TEST_ASSERT_SQL_FILES="sql/assertions/after_restore.sql"
UTILITY_TEST_ASSERT_SHELL='test -s "$REPO_DIR/logs/utility/custom.log"'
UTILITY_TEST_SCAN_PATHS="logs/utility generated/tool-output"
```

Expected files are converted into host-shell non-empty file assertions, while
SQL assertions remain declarative. `UTILITY_TEST_EXPECT_FILES` and
`UTILITY_TEST_ASSERT_SHELL` therefore require the explicit
`UTILITY_TEST_TRUSTED_SHELL=1` marker, which is mapped to the experiment
runner's trust gate. The marker is not required for SQL-only assertions.

Batch utility tests with utility-suite specs:

```bash
make utility-suite-list
make utility-suite-show UTILITY_SUITE=native-dump
make utility-suite-plan UTILITY_SUITE=native-dump
make utility-suite-plan-json UTILITY_SUITE=native-dump
make utility-suite-run UTILITY_SUITE=native-dump
make utility-suite-run-json UTILITY_SUITE=native-dump
make utility-suite-run-list
make utility-suite-run-show UTILITY_SUITE_RUN=<suite-run-id>
make utility-suite-run-bundle UTILITY_SUITE_RUN=<suite-run-id> UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz
make utility-suite-run-bundle-json UTILITY_SUITE_RUN=<suite-run-id> UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz
make utility-suite-run-verify UTILITY_SUITE_RUN=<suite-run-id>
make utility-suite-run-verify-json UTILITY_SUITE_RUN=<suite-run-id>
```

Suites expand utility tests across profile sizes and repeat counts, write
`runs.tsv`, `result.json`, `summary.md`, and driver logs under
`runs/utility-suites/<suite-run-id>/`, and keep individual utility-test
artifacts under normal `runs/<run-id>/` directories. `run-verify` checks the
suite artifact structure and verifies linked experiment run artifacts when they
exist; a failed utility test can still be a valid artifact if its evidence is
complete. `run-bundle` writes a tar.gz containing the suite artifact under
`utility-suites/<suite-run-id>/` and linked experiment artifacts under
`runs/<run-id>/`.

## Dump And Restore

Preview native PostgreSQL utility scenarios:

```bash
make utility-plan UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan UTILITY_TEST_SPEC=pg-restore/smoke
make utility-plan UTILITY_TEST_SPEC=pg-dumpall/smoke
make utility-plan-expanded UTILITY_TEST_SPEC=pg-dumpall/wal-pressure
make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
make utility-suite-plan UTILITY_SUITE=native-dump
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

The built-in `pg-dump`, `pg-dumpall`, and `pg-restore` workload adapters are
runtime-neutral. Docker runs the matching client in the workbench-owned
PostgreSQL service; native mode resolves it through
`PGWORKBENCH_NATIVE_BINDIR`. Output paths are fail-closed to the disposable
`logs/utility/` and `.tmp/utility-output/` roots; a utility spec cannot replace
source files or Git metadata.

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
make utility-suite-run-list
make utility-suite-run-show UTILITY_SUITE_RUN=<suite-run-id>
make utility-suite-run-bundle UTILITY_SUITE_RUN=<suite-run-id> UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz
make utility-suite-run-verify UTILITY_SUITE_RUN=<suite-run-id>
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
