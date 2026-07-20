# massive-dml

Reproducible PostgreSQL scenarios for large `UPDATE` and `DELETE` operations
using explicit committed batches.

The safe unit of work is a committed batch, not a loop iteration. The primary
operational path is generated SQL:

```text
generate -> review -> execute -> log -> stop/resume -> validate
```

The profile uses deterministic synthetic data and owns only the
`massive_dml` schema. Sizes are intentionally bounded:

| Size | Transaction rows | Audit rows | Old audit rows |
| --- | ---: | ---: | ---: |
| `small` | 5,000 | 3,000 | 1,200 |
| `medium` | 50,000 | 30,000 | 12,000 |
| `large` | 1,000,000 | 500,000 | 250,000 |

## Verified experiments

```bash
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-update
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-update
make experiment-run EXPERIMENT_SPEC=massive-dml/queue-update
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/transaction-caveats
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-indexed
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-index-after
make experiment-run EXPERIMENT_SPEC=massive-dml/partition-drop-vs-delete
```

Generated SQL, metadata, and final profile statistics are written under the
experiment's `artifacts/` directory. Every experiment also captures the normal
workbench manifest, workload log, metrics, snapshots, assertions, scan, and
verdict.

Run the initial parity matrix:

```bash
make matrix-plan MATRIX_SPEC=massive-dml-comparison
make matrix-run MATRIX_SPEC=massive-dml-comparison
```

Run the focused strategy series with three medium-size repeats:

```bash
make matrix-plan MATRIX_SPEC=massive-dml-strategy
make matrix-run MATRIX_SPEC=massive-dml-strategy
```

The bulk-load runs preserve `load_ms`, `index_ms`, `total_ms`, WAL bytes, and
relation sizes. The partition comparison builds identical inputs and records
the row DELETE and partition detach/drop results in one artifact. These are
local comparative measurements, not universal production forecasts.

Increase `MATRIX_REPEATS` before treating timing differences as evidence:

```bash
MATRIX_REPEATS=3 MATRIX_PROFILE_SIZES=medium \
  make matrix-run MATRIX_SPEC=massive-dml-comparison
```

## Inspect and diagnose

```bash
make profile-reset PROFILE=massive-dml PROFILE_SIZE=small
make profile-run PROFILE=massive-dml WORKLOAD_SQL=70_diagnostics.sql
make monitor
```

`10_run.sql` is a safe representative `EXPLAIN (ANALYZE, BUFFERS)` executed
inside `BEGIN ... ROLLBACK`. It demonstrates real write cost without retaining
the changes.

Optional noisia pressure remains a workbench concern rather than profile
infrastructure:

```bash
MASSIVE_DML_BACKGROUND_SPECS=noisia/wait-xacts \
  make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-update
```

## Manual generated-first flow

For a real review boundary, generate without executing:

```bash
make profile-setup PROFILE=massive-dml PROFILE_SIZE=medium
MASSIVE_DML_EXECUTE=0 \
  make workload-run-shell WORKLOAD_SPEC=massive-dml/generated-update
less generated/massive-dml/generated-update.sql
./scripts/psql.sh -f generated/massive-dml/generated-update.sql
```

Stopping `psql` rolls back only the open batch. Already committed batches stay
committed; regenerate from current database state to resume.

## Supporting guidance

- [Generated-first runbook](docs/generated-first-runbook.md)
- [Production checklist](docs/production-checklist.md)
- [Demo flow](docs/demo-script.md)
- [v0.1.37 candidate demo results](docs/demo-results-v0.1.37-rc.md)
- [When not to use row DELETE](docs/when-not-to-use-row-delete.md)

This profile is an engineering lab, not a production migration package. Always
replace its synthetic predicates, indexes, time semantics, timeouts, and stop
criteria with values validated against the real system.
