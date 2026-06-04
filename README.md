# PostgreSQL Experiment Workbench

Reusable local PostgreSQL workspace for experiments, utility testing, workload
generation, monitoring, and reproducible profile-based demos.

This project is a generic platform. Domain-specific labs should live as
profiles or separate focused repositories.

## Core Shape

```text
Docker PostgreSQL
-> profile setup SQL
-> optional workload/noise
-> monitoring
-> logs
-> repeatable teardown/reset
```

## Quick Start

```bash
make docker-reset
make profile-reset PROFILE=smoke PROFILE_SIZE=small
make monitor
```

Run one of the starter experiment profiles:

```bash
make profile-reset PROFILE=locks PROFILE_SIZE=small
make profile-reset PROFILE=vacuum-bloat PROFILE_SIZE=small
make profile-reset PROFILE=indexes PROFILE_SIZE=small
make profile-reset PROFILE=wal-pressure PROFILE_SIZE=small
make profile-reset PROFILE=partitioning PROFILE_SIZE=small
make profile-reset PROFILE=temp-spill PROFILE_SIZE=small
```

Open psql:

```bash
make psql
```

Connection URL:

```text
postgres://postgres:postgres@127.0.0.1:55433/pg_experiment_workbench?sslmode=disable
```

## Profiles

Profiles live under:

```text
profiles/<profile-name>/
```

Expected SQL files:

```text
profiles/<profile-name>/sql/00_setup.sql
profiles/<profile-name>/sql/10_run.sql
```

Run:

```bash
make profile-list
make profile-show PROFILE=locks
make profile-reset PROFILE=smoke PROFILE_SIZE=small
```

Run a specific profile SQL file:

```bash
make profile-run PROFILE=locks WORKLOAD_SQL=30_diagnostics.sql
```

Profiles should be self-contained and safe to reset in a local disposable
database.

Profile authoring guidance lives in [docs/profile-authoring.md](docs/profile-authoring.md).

## Workloads

The generic workload runner can execute SQL, profile SQL, `pgbench`, noisia,
PostgreSQL source checks, host shell commands, or arbitrary Docker images:

```bash
make workload-list
make workload-show WORKLOAD_SPEC=pgbench/tiny
make workload-run WORKLOAD_SPEC=pgbench/tiny
make workload-run WORKLOAD_SPEC=compose/pg-isready
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

Workload platform details live in [docs/workload-platform.md](docs/workload-platform.md).

## Topologies

Runtime topologies describe the PostgreSQL shape an experiment needs:

```bash
make topology-list
make topology-up TOPOLOGY=primary-replica
make topology-status TOPOLOGY=primary-replica
```

Implemented topologies:

- `single`: one disposable PostgreSQL container.
- `primary-replica`: physical streaming replica with a local replication slot.
- `logical-replication`: publisher plus independent logical subscriber.
- `pgbouncer`: PostgreSQL plus PgBouncer pooler.
- `multi-version-upgrade`: old and new PostgreSQL versions for upgrade tests.

## Experiments

Experiments orchestrate datasets, profiles, workloads, background pressure,
metrics, snapshots, assertions, scans, and verdicts into `runs/<run-id>/`:

```bash
make experiment-list
make experiment-run EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=locks-under-contention
make experiment-run EXPERIMENT_SPEC=replica-readonly
make experiment-run EXPERIMENT_SPEC=logical-replication
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
make experiment-run EXPERIMENT_SPEC=temp-spill
make experiment-report RUN_DIR=runs/<run-id>
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
make experiment-summary SUMMARY_INPUT=runs/repeats/<repeat-id>
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
```

Experiment platform details live in [docs/experiment-platform.md](docs/experiment-platform.md).

For batches:

```bash
make matrix-list
make matrix-plan MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
```

## Datasets

Reusable data-loading specs live under `datasets/`:

```bash
make dataset-list
make dataset-load DATASET_SPEC=synthetic/items DATASET_SIZE=small
```

Noisia can be used as optional PostgreSQL pressure:

```bash
NOISIA_DURATION=120 NOISIA_JOBS=4 make noisia-wait
NOISIA_DURATION=120 NOISIA_JOBS=2 make noisia-temp
```

Noisia is intentionally harmful test tooling. Use it only against disposable
local databases.

For profile-local SQL that should run in the background:

```bash
make workload-start PROFILE=locks WORKLOAD_SQL=20_blocker.sql PROFILE_SECONDS=60
make workload-status
make workload-log
make workload-stop
```

Any workload spec can also run in the background:

```bash
make workload-start-spec WORKLOAD_SPEC=profile/locks-blocker PROFILE_SECONDS=60
```

Noisia can also run through the background helper:

```bash
make workload-start-noisia WORKLOAD=wait-xacts NOISIA_DURATION=120 NOISIA_JOBS=4
```

## Metrics

Sample broad PostgreSQL metrics to CSV:

```bash
METRICS_DURATION=30 METRICS_INTERVAL=1 make metrics-sample
```

Metrics are written under:

```text
logs/metrics/
```

## Failure Scanning

Scan logs and generated artifacts for PostgreSQL crash/error evidence:

```bash
make scan-artifacts
./scripts/scan_pg_failures.sh logs generated
```

## CI

Default CI runs `make check`, `make test`, and `make scan-artifacts`.
PostgreSQL source-tree checks are manual/opt-in. Details live in
[docs/ci.md](docs/ci.md).

## Go CLI

The first Go command is available for profile catalog operations:

```bash
go run ./cmd/pgworkbench profile list
go run ./cmd/pgworkbench profile validate
```

Go migration notes live in [docs/go-migration.md](docs/go-migration.md).

## Logs

Run any SQL file with logging:

```bash
make run-sql SQL=profiles/smoke/sql/10_run.sql
```

Logs are written to:

```text
logs/
```

## First Intended Profiles

- `smoke`: minimal profile proving the platform works.
- `locks`: lock waits, blockers, blocked sessions.
- `vacuum-bloat`: dead tuples, vacuum behavior, bloat.
- `indexes`: index creation, query plans, write overhead.
- `wal-pressure`: WAL-heavy writes and checkpoint pressure.
- `partitioning`: partition attach/detach/drop experiments.
- `temp-spill`: sort/hash spills and temporary file counters.
- `replication-slots`: physical slot retention and streaming state.
- `logical-replication`: publication/subscription convergence and DDL boundary checks.
- `connection-pressure`: session churn and pooler-shaped behavior.

Massive-DML-specific work belongs in the separate focused repository unless a
small generic scenario is useful here.

## Status

Generic scaffold with first real profiles, workload adapters, background
helpers, CSV metric sampling, experiment reports, repeat runs, matrix execution,
and statistical run-series summaries. Keep specialized explanations in focused
profile docs and workload specs.
