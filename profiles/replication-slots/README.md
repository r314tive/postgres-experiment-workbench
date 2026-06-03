# replication-slots Profile

Generic profile for observing physical replication slots, retained WAL, and
streaming replication state.

It is most useful with:

```bash
make topology-up TOPOLOGY=primary-replica
make profile-reset PROFILE=replication-slots
```

The profile is intentionally small. It creates WAL and reports slot retention
without assuming a production failover manager or a specific replication
operator.
