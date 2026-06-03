# logical-replication Profile

Generic profile for publication/subscription experiments.

It deliberately keeps DDL explicit on both sides of replication. Logical
replication does not replicate table creation, so subscriber schema setup is a
topology/workload concern rather than hidden profile behavior.

Run:

```bash
make topology-up TOPOLOGY=logical-replication
make profile-setup PROFILE=logical-replication
./scripts/setup_logical_replication.sh
make profile-run PROFILE=logical-replication
./scripts/wait_logical_replication.sh
```
