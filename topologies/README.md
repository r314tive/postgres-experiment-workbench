# Topologies

Topology support describes which PostgreSQL runtime shape an experiment expects.

Current implemented topology:

- `single`: one disposable PostgreSQL container plus optional workload sidecars.

Planned topology specs can live here without changing profile or workload
contracts:

- `primary-replica`
- `logical-replication`
- `pgbouncer`
- `multi-version-upgrade`

The experiment runner records `EXPERIMENT_TOPOLOGY` in each run manifest. Runtime
implementation can expand from this directory without changing experiment specs.
