# Partitioning Profile

Demonstrates range partition pruning plus attach, detach, and drop mechanics for
a short-lived partition.

## Run

```bash
make profile-reset PROFILE=partitioning PROFILE_SIZE=small
make monitor
```

## What It Shows

- a range-partitioned event table;
- partition pruning for date predicates;
- attaching a staged table as a new partition;
- detaching and dropping the staged partition.
