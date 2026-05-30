# Indexes Profile

Demonstrates plan changes after adding a targeted index and shows basic index
size and usage counters.

## Run

```bash
make profile-reset PROFILE=indexes PROFILE_SIZE=small
make monitor
```

## What It Shows

- query plan before a secondary index exists;
- query plan after adding a composite index;
- index size and `pg_stat_user_indexes` counters;
- write-path shape for an insert probe that is rolled back.
