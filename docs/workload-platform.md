# Workload Platform

This repository should act as a generic PostgreSQL experiment platform, not a
single-purpose lab.

The platform has five layers:

1. PostgreSQL runtime: Docker Compose starts a disposable PostgreSQL instance.
2. Profiles: SQL creates repeatable database states.
3. Workload specs: adapters run SQL, `pgbench`, noisia, shell commands, or
   arbitrary Docker images against the database.
4. Observation: monitor SQL, CSV metrics, logs, and profile diagnostics capture
   behavior.
5. Utility tests: external tools can run as shell or container workloads while
   the database state and metrics are controlled by profiles.

## Supported Workload Shapes

`scripts/run_workload.sh` supports these adapter kinds:

- `profile-sql`: profile-local SQL files.
- `sql`: any repo-local SQL file.
- `pgbench`: standard PostgreSQL benchmark client inside the postgres
  container.
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

For load generation with built-in PostgreSQL tooling:

```bash
PGBENCH_TIME=30 PGBENCH_CLIENTS=8 make workload-run WORKLOAD_SPEC=pgbench/tiny
```

For failure-injection pressure:

```bash
NOISIA_DURATION=120 NOISIA_JOBS=4 make workload-run WORKLOAD_SPEC=noisia/wait-xacts
```

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
