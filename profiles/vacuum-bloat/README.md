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
