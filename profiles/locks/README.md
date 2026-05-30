# Locks Profile

Demonstrates table locks, advisory locks, row lock contention, and blocking
diagnostics.

## Run

```bash
make profile-reset PROFILE=locks PROFILE_SIZE=small
make monitor
```

## Contention Workflow

Start a bounded row-lock holder in the background, then run the waiter and
diagnostics from the foreground:

```bash
make workload-start PROFILE=locks WORKLOAD_SQL=20_blocker.sql PROFILE_SECONDS=60
make profile-run PROFILE=locks WORKLOAD_SQL=21_waiter.sql
make run-sql SQL=profiles/locks/sql/30_diagnostics.sql
make workload-stop
```

The waiter catches the expected lock timeout and emits a notice instead of
failing the run.

## What To Inspect

- `pg_stat_activity.wait_event_type`
- `pg_blocking_pids(pid)`
- ungranted rows in `pg_locks`
- `make metrics-sample` columns for lock waits and blocked sessions
