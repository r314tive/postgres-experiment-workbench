# logical-ddl Profile

Topology-aware profile for logical replication DDL boundary experiments.

Logical replication does not replicate DDL. This profile keeps that behavior
visible:

- create a baseline published table;
- add a publisher-side column and table;
- verify the subscriber did not receive that DDL automatically;
- apply matching subscriber DDL explicitly;
- refresh the subscription publication set;
- insert/update/delete rows and verify publisher/subscriber checksums.

Run the full experiment:

```bash
make experiment-run EXPERIMENT_SPEC=logical-ddl
```

Manual flow:

```bash
make topology-reset TOPOLOGY=logical-replication
make profile-setup PROFILE=logical-ddl
./scripts/setup_logical_replication.sh
make workload-run WORKLOAD_SPEC=profile/logical-ddl-run
LOGICAL_REPLICATION_COMPARE_SQL="SELECT 'events', count(*), coalesce(sum(id), 0), coalesce(sum(length(payload)), 0), coalesce(sum(length(ddl_marker)), 0) FROM logical_repl.events UNION ALL SELECT 'ddl_notes', count(*), coalesce(sum(id), 0), coalesce(sum(length(note)), 0), 0 FROM logical_repl.ddl_notes ORDER BY 1" ./scripts/wait_logical_replication.sh
```
