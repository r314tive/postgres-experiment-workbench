# Topologies

Topology support describes which PostgreSQL runtime shape an experiment expects.

Implemented topologies:

- `single`: one disposable PostgreSQL container plus optional workload sidecars.
- `primary-replica`: primary plus one physical streaming replica using a local
  physical replication slot.
- `logical-replication`: one publisher plus one independent logical subscriber.
- `pgbouncer`: PostgreSQL plus PgBouncer pooler.

Planned topology specs can live here without changing profile or workload
contracts:

- `pgbouncer`
- `multi-version-upgrade`

The experiment runner records `EXPERIMENT_TOPOLOGY` in each run manifest. Runtime
implementation can expand from this directory without changing experiment specs.

Run:

```bash
make topology-list
make topology-up TOPOLOGY=primary-replica
make topology-status TOPOLOGY=primary-replica
make topology-reset TOPOLOGY=primary-replica
make topology-up TOPOLOGY=logical-replication
make topology-up TOPOLOGY=pgbouncer
```
