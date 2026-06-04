# Topologies

Topology support describes which PostgreSQL runtime shape an experiment expects.

Implemented topologies:

- `single`: one disposable PostgreSQL container plus optional workload sidecars.
- `primary-replica`: primary plus one physical streaming replica using a local
  physical replication slot.
- `logical-replication`: one publisher plus one independent logical subscriber.
- `pgbouncer`: PostgreSQL plus PgBouncer pooler.
- `multi-version-upgrade`: old and new PostgreSQL versions for upgrade-path
  utility tests.

The experiment runner records `EXPERIMENT_TOPOLOGY` in each run manifest. Runtime
implementation can expand from this directory without changing experiment specs.

Run:

```bash
make topology-list
make topology-inspect TOPOLOGY=primary-replica
make topology-up TOPOLOGY=primary-replica
make topology-ps TOPOLOGY=primary-replica
make topology-status TOPOLOGY=primary-replica
make topology-reset TOPOLOGY=primary-replica
make topology-up TOPOLOGY=logical-replication
make topology-up TOPOLOGY=pgbouncer
make topology-up TOPOLOGY=multi-version-upgrade
```

`make topology-inspect` is a no-Docker Go preflight. It renders the topology
spec path, env file, Compose command, required profiles, services, and resolved
topology variables. `make topology-ps` parses live `docker compose ps --format
json` output into a stable service summary after a topology has been started.
Use `make topology-status` for richer live container and SQL status.
