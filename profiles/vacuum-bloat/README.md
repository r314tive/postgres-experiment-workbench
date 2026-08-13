# Vacuum Bloat Profile

Demonstrates dead tuples, table statistics, and a bounded manual vacuum cycle.

## Run

```bash
make profile-reset PROFILE=vacuum-bloat PROFILE_SIZE=small
make monitor
```

## What It Shows

- dead tuple creation through committed updates and deletes;
- `pg_stat_user_tables` counters before and after `VACUUM`;
- relation size checks with `pg_total_relation_size`;
- a selective query plan after churn.

Autovacuum is disabled on the profile table so the run remains deterministic.
This is for local disposable experiments only.

The descriptive operation pack `maintenance/vacuum-bloat-manual` rebuilds a
medium profile for every exact trial, creates committed churn, and records a
server-clock interval bracketing `VACUUM (ANALYZE)`. Its documented scope
includes the psql protocol gaps between the bracketing commands and excludes
churn; it is not a pure executor timer or production-autovacuum claim.
