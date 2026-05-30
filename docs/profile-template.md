# <Profile Name>

Short description of the PostgreSQL behavior this profile demonstrates.

## Run

```bash
make profile-reset PROFILE=<profile-name> PROFILE_SIZE=small
make monitor
```

## What It Shows

- Behavior or subsystem under test.
- Main tables, indexes, or background sessions involved.
- Counters or views worth inspecting.

## Optional Workflow

```bash
make workload-start PROFILE=<profile-name> WORKLOAD_SQL=<sql-file> PROFILE_SECONDS=60
make workload-status
make workload-log
make workload-stop
```

## Metrics

```bash
METRICS_DURATION=30 METRICS_INTERVAL=1 make metrics-sample
```

## Notes

Keep profile-specific warnings, assumptions, and interpretation guidance here.
