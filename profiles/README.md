# Profiles

A profile is a self-contained PostgreSQL experiment scenario.

Recommended structure:

```text
profiles/<name>/
  profile.env
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

Machine-readable metadata is optional but recommended:

```bash
make profile-list
make profile-show PROFILE=locks
make profile-validate
```

See [../docs/profile-authoring.md](../docs/profile-authoring.md) for conventions
and [../docs/profile-template.md](../docs/profile-template.md) for a README
starter.

## Catalog

| Profile | Purpose |
| --- | --- |
| `smoke` | Minimal platform verification. |
| `constraints` | Constraint validation, deferrable foreign keys, uniqueness, checks. |
| `jsonb` | JSONB containment, expression indexes, partial indexes, update shape. |
| `locks` | Table locks, advisory locks, row lock contention, blocking diagnostics. |
| `vacuum-bloat` | Dead tuples, relation statistics, manual vacuum. |
| `indexes` | Plan changes, index size, write-path probe. |
| `wal-pressure` | WAL deltas across bounded write phases. |
| `partitioning` | Range partition pruning, attach, detach, drop. |
| `temp-spill` | Sort/hash spills and temporary file counters. |
| `replication-slots` | Physical slot retention and streaming replication state. |
| `logical-replication` | Publication/subscription convergence and DDL boundary checks. |
| `connection-pressure` | Session churn, backend reuse, and pooler-shaped behavior. |
