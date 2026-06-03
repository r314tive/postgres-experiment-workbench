# Profiles

A profile is a self-contained PostgreSQL experiment scenario.

Recommended structure:

```text
profiles/<name>/
  README.md
  sql/
    00_setup.sql
    10_run.sql
```

The workbench passes these psql variables:

```text
:profile
:profile_size
:profile_seconds
```

Keep profiles resettable and safe for disposable local databases. Default
`small` runs should stay quick; `medium` and `large` may be slower but should
still be bounded.

See [../docs/profile-authoring.md](../docs/profile-authoring.md) for conventions
and [../docs/profile-template.md](../docs/profile-template.md) for a README
starter.

## Catalog

| Profile | Purpose |
| --- | --- |
| `smoke` | Minimal platform verification. |
| `locks` | Table locks, advisory locks, row lock contention, blocking diagnostics. |
| `vacuum-bloat` | Dead tuples, relation statistics, manual vacuum. |
| `indexes` | Plan changes, index size, write-path probe. |
| `wal-pressure` | WAL deltas across bounded write phases. |
| `partitioning` | Range partition pruning, attach, detach, drop. |
| `replication-slots` | Physical slot retention and streaming replication state. |
| `logical-replication` | Publication/subscription convergence and DDL boundary checks. |
