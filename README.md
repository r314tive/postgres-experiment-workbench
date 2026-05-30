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
```

Workload platform details live in [docs/workload-platform.md](docs/workload-platform.md).

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

Massive-DML-specific work belongs in the separate focused repository unless a
small generic scenario is useful here.

## Status

Generic scaffold with first real profiles, workload adapters, background
helpers, and CSV metric sampling. Keep specialized explanations in focused
profile docs and workload specs.
