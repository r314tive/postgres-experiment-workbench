# Workloads

Workloads are executable pressure, utility, or verification actions that target
the workbench PostgreSQL instance.

Profiles answer "what data/state should exist?" Workloads answer "what should
run against that state?"

## Run

```bash
make workload-list
make workload-show WORKLOAD_SPEC=pgbench/tiny
make workload-run WORKLOAD_SPEC=pgbench/tiny
```

Specs live under `workloads/**/*.env`. They are trusted local shell env files
loaded by `scripts/run_workload.sh`.

## Adapter Kinds

| Kind | Purpose |
| --- | --- |
| `profile-sql` | Run SQL from `profiles/<profile>/sql/`. |
| `sql` | Run any repo-local SQL file through psql. |
| `pgbench` | Run `pgbench` inside the postgres container. |
| `noisia` | Run noisia through the existing Docker Compose wrapper. |
| `shell` | Run any host command with PG env vars and `DATABASE_URL`. |
| `compose-run` | Run any Docker image as a one-shot workload container. |

## Spec Shape

```bash
WORKLOAD_NAME="example"
WORKLOAD_KIND="shell"
WORKLOAD_CMD='echo "$DATABASE_URL"'
```

Common variables:

```text
PROFILE_SIZE=small
PROFILE_SECONDS=30
WORKLOAD_RUN_LOG=1
WORKLOAD_LOG_DIR=logs/workloads
```

Shell and compose workloads receive standard connection settings:

```text
PGHOST
PGPORT
PGDATABASE
PGUSER
PGPASSWORD
DATABASE_URL
```

## Boundaries

The runner is intentionally permissive. Use it for disposable local
experiments, external utility tests, data checks, PostgreSQL behavior tests, and
failure injection. Keep destructive or heavyweight workflows explicit in the
spec README or comments.
