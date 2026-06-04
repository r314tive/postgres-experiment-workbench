# Workload Platform

This repository should act as a generic PostgreSQL experiment platform, not a
single-purpose lab.

The platform has six layers:

1. PostgreSQL runtime: Docker Compose starts a disposable PostgreSQL instance.
2. Topologies: runtime shape such as `single` or `primary-replica`.
3. Profiles: SQL creates repeatable database states.
4. Workload specs: adapters run SQL, `pgbench`, noisia, shell commands, or
   arbitrary Docker images against the database.
5. Observation: monitor SQL, CSV metrics, logs, and profile diagnostics capture
   behavior.
6. Utility tests: external tools can run as shell or container workloads while
   the database state and metrics are controlled by profiles.

## Supported Workload Shapes

`scripts/run_workload.sh` supports these adapter kinds:

- `profile-sql`: profile-local SQL files.
- `sql`: any repo-local SQL file.
- `pgbench`: standard PostgreSQL benchmark client inside the postgres
  container.
- `pg-source-check`: clone, patch, build, test, and scan a PostgreSQL source
  tree.
- `noisia`: harmful PostgreSQL workload generator through the existing wrapper.
- `shell`: any host command with PG environment variables.
- `compose-run`: any Docker image with PG environment variables.

Noisia is useful for failure-injection style pressure such as waiting
transactions, deadlocks, temporary files, connection exhaustion, cancelled
queries, and idle transactions. See the upstream project:
<https://github.com/lesovsky/noisia>.

## External Utility Testing

For a utility installed on the host:

```bash
make profile-reset PROFILE=smoke
make workload-run WORKLOAD_SPEC=shell/pg-dump-smoke
```

For a utility distributed as a container image:

```bash
WORKLOAD_IMAGE=postgres:16-alpine \
WORKLOAD_COMMAND='pg_isready -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE"' \
make workload-run WORKLOAD_SPEC=compose/pg-isready
```

For a third-party workload or fuzzing tool, use an opt-in template and provide
the image and command that match your local packaging:

```bash
SQLSMITH_IMAGE=your/sqlsmith:tag \
SQLSMITH_COMMAND='sqlsmith --target "$DATABASE_URL"' \
make workload-run WORKLOAD_SPEC=external/sqlsmith
```

For load generation with built-in PostgreSQL tooling:

```bash
PGBENCH_TIME=30 PGBENCH_CLIENTS=8 make workload-run WORKLOAD_SPEC=pgbench/tiny
```

For failure-injection pressure:

```bash
NOISIA_DURATION=120 NOISIA_JOBS=4 make workload-run WORKLOAD_SPEC=noisia/wait-xacts
```

For replica-aware utility checks:

```bash
make topology-up TOPOLOGY=primary-replica
make workload-run WORKLOAD_SPEC=topology/replica-readonly
make workload-run WORKLOAD_SPEC=topology/replication-status
make topology-up TOPOLOGY=logical-replication
make workload-run WORKLOAD_SPEC=topology/logical-status
make topology-up TOPOLOGY=pgbouncer
make workload-run WORKLOAD_SPEC=topology/pgbouncer-admin
make topology-up TOPOLOGY=multi-version-upgrade
make workload-run WORKLOAD_SPEC=topology/upgrade-status
make workload-run WORKLOAD_SPEC=topology/upgrade-dump-restore
```

For testing PostgreSQL source itself:

```bash
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
make workload-run WORKLOAD_SPEC=pg-source/check
PG_PATCH_DIR=patchsets/chaos/master make workload-run WORKLOAD_SPEC=pg-source/chaos-check
```

The source-check adapter follows the same discipline as other workloads: a spec
declares the work, logs and artifacts are stored under ignored local folders,
and `scripts/scan_pg_failures.sh` provides the generic verdict layer.

External templates live under `workloads/external/`. They do not vendor or pin
third-party projects; they provide the execution contract, PostgreSQL connection
environment, logging, metrics, snapshots, and experiment verdict handling around
those projects.

## Experiments

Workloads can be run directly, but repeatable test scenarios should use the
experiment layer:

```bash
make experiment-run EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=locks-under-contention
make experiment-run EXPERIMENT_SPEC=replica-readonly
make experiment-run EXPERIMENT_SPEC=logical-replication
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
make matrix-plan MATRIX_SPEC=smoke
```

See [experiment-platform.md](experiment-platform.md).

## Failure Scanning

Scan local logs, PostgreSQL regression artifacts, copied test output, or source
check output:

```bash
make scan-artifacts
make scan-artifacts SCAN_PATHS='logs generated'
./scripts/scan_pg_failures.sh /path/to/pg/test/output
```

The scanner looks for core files, assertion/crash patterns, regression diff
errors, sanitizer output, and Valgrind error summaries.

## Background Workloads

Any workload spec can be started in the background:

```bash
make workload-start-spec WORKLOAD_SPEC=profile/locks-blocker PROFILE_SECONDS=60
make workload-status
make workload-log
make workload-stop
```

Use this for utility tests that need concurrent pressure: start a blocker or
load generator, run the external tool, then sample metrics and inspect logs.

## Design Rule

Keep the platform generic by putting tool-specific behavior in workload specs
and profile-specific PostgreSQL interpretation in profile READMEs. The root
README should remain about mechanics.
