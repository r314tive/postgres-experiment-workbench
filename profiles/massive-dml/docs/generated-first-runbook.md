# Generated-first massive DML runbook

Generated SQL is the preferred default for one-time maintenance because the
exact executable file can be reviewed before execution. Each batch exposes its
own transaction, timeouts, timing output, and post-commit sleep.

## Automated evidence run

```bash
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-update
make run-list RUN_LIMIT=5
make run-show RUN_DIR=runs/<run-id>
make experiment-verify RUN_DIR=runs/<run-id>
make experiment-report RUN_DIR=runs/<run-id>
```

Inspect `runs/<run-id>/artifacts/generated-update.sql`. The experiment has
already executed it against disposable profile data and verified that:

- every `BEGIN` has a matching `COMMIT`;
- all backfillable rows were processed;
- source-null control rows were preserved;
- no target timestamp was populated from a null source.

The generated DELETE experiment additionally freezes its cutoff and verifies
that fresh rows remain.

## Review-before-execute flow

Prepare data and generate without executing:

```bash
make profile-setup PROFILE=massive-dml PROFILE_SIZE=medium
MASSIVE_DML_EXECUTE=0 MASSIVE_DML_BATCH_SIZE=5000 \
  make workload-run-shell WORKLOAD_SPEC=massive-dml/generated-update
```

Review and execute:

```bash
less generated/massive-dml/generated-update.sql
./scripts/psql.sh -f generated/massive-dml/generated-update.sql
./scripts/run_profile_sql.sh massive-dml 80_assert_update_complete.sql
```

For DELETE:

```bash
MASSIVE_DML_EXECUTE=0 \
MASSIVE_DML_CUTOFF='2026-04-07 00:00:00+00' \
  make workload-run-shell WORKLOAD_SPEC=massive-dml/generated-delete
less generated/massive-dml/generated-delete.sql
./scripts/psql.sh -f generated/massive-dml/generated-delete.sql
./scripts/run_profile_sql.sh massive-dml 81_assert_delete_complete.sql
```

## Stop and resume

Stop the active `psql` process. The current open transaction rolls back while
earlier batches remain committed. Keep the workload log, count remaining work
from the database, then regenerate the file. The generators select only rows
that still match the idempotent predicate.

Do not wrap the whole generated file in an external transaction. Sleep belongs
after `COMMIT`, not inside one long transaction.

## Procedure alternative

```bash
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-update
```

The procedure commits internally and therefore must be called at top level.
It is useful for reusable database-side behavior, but its executable sequence
is less reviewable than the generated file.

Additional parity paths are available as normal experiments:

```bash
make experiment-run EXPERIMENT_SPEC=massive-dml/queue-update
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/transaction-caveats
```

The transaction-caveats experiment passes only when both unsafe forms fail as
expected and their error logs are preserved as artifacts.
