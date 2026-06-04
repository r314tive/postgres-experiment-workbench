# jsonb

Generic JSONB behavior profile.

It creates a disposable event table with structured `jsonb` documents and then
exercises:

- containment predicates;
- GIN `jsonb_path_ops`;
- expression indexes on JSONB fields;
- partial indexes derived from JSONB fields;
- rolled-back `jsonb_set` updates.

Useful for testing tools that inspect JSONB-heavy schemas, preserve expression
indexes, compare query plans, dump/restore JSONB values, or validate metadata
around generated document shapes.

Run:

```bash
make profile-reset PROFILE=jsonb PROFILE_SIZE=small
make profile-run PROFILE=jsonb WORKLOAD_SQL=30_diagnostics.sql
make experiment-run EXPERIMENT_SPEC=jsonb-indexing
```
