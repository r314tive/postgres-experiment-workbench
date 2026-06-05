# Diagnostics

Diagnostics are read-only SQL snippets for quickly inspecting a local
PostgreSQL experiment target. They are not a replacement for profile-specific
assertions; they are small reusable DBA views into the current runtime state.

Run:

```bash
make diagnostics-list
make diagnostics-show DIAGNOSTIC=activity
make diagnostics-run DIAGNOSTIC=activity
```

Available diagnostics:

- `activity`: active sessions, wait events, transaction/query age, and query
  text preview.
- `locks`: blocking relationships from `pg_blocking_pids()` plus lock summary.
- `settings`: non-default or pending restart settings from `pg_settings`.
- `table_health`: live/dead tuple counters, vacuum/analyze counters, and table
  size.
- `index_health`: index scan counters, table write counters, index size, and a
  simple observation signal.
- `wal`: WAL and checkpoint counters.
- `replication`: replication sender and slot state.

The catalog is intentionally generic and read-only. It was inspired by common
DBA diagnostic categories found in public PostgreSQL utility collections such as
Data Egret's `pg-utils`, but the snippets here are local workbench queries and
should stay small, portable, and profile-neutral:
<https://github.com/dataegret/pg-utils>.
