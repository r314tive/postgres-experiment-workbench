# Demo flow

## 1. Show the safe unit of work

```bash
make experiment-plan-expanded \
  EXPERIMENT_SPEC=massive-dml/generated-batched-update
make experiment-run \
  EXPERIMENT_SPEC=massive-dml/generated-batched-update
```

Open the generated SQL artifact and point out explicit `BEGIN`, `COMMIT`,
per-batch timeouts, and sleep after commit.

## 2. Show evidence

```bash
make run-list RUN_LIMIT=5
make experiment-report RUN_DIR=runs/<run-id>
make experiment-verify-json RUN_DIR=runs/<run-id>
```

Show the manifest, workload log, metrics, before/after snapshots, generated SQL,
assertions, and verdict.

## 3. Show DELETE with a fixed cutoff

```bash
make experiment-run \
  EXPERIMENT_SPEC=massive-dml/generated-batched-delete
```

The selector uses `ORDER BY created_at, audit_record_id`, `LIMIT`, and
`FOR UPDATE SKIP LOCKED` with a matching index.

## 4. Show procedure transaction control

```bash
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-update
```

Explain that internal `COMMIT` requires a top-level `CALL`. Load
`60_transaction_caveats.sql` manually to demonstrate why a temporary table with
`ON COMMIT DROP` cannot survive an internal commit loop.

## 5. Choose an offline load strategy

```bash
make experiment-run \
  EXPERIMENT_SPEC=massive-dml/offline-bulk-load-indexed
make experiment-run \
  EXPERIMENT_SPEC=massive-dml/offline-bulk-load-index-after
```

Compare the `bulk-load-*.tsv` artifacts. Separate load time from index build
time, then compare total time and WAL; do not generalize from one run.

## 6. Choose row DELETE or partition removal

```bash
make experiment-run \
  EXPERIMENT_SPEC=massive-dml/partition-drop-vs-delete
```

The two variants start from identical partitioned data and must remove and
retain the same row counts. Use the artifact to discuss why a retention-aligned
partition operation changes the physical problem.

## 7. Repeat before concluding

```bash
MATRIX_PROFILE_SIZES=medium MATRIX_REPEATS=3 \
  make matrix-run MATRIX_SPEC=massive-dml-strategy
```

Show `summary.md`, `statistics.md`, and the per-run domain artifacts together.

## 8. Optional pressure

```bash
MASSIVE_DML_BACKGROUND_SPECS=noisia/wait-xacts \
MASSIVE_DML_BACKGROUND_WARMUP=2 \
  make experiment-run \
    EXPERIMENT_SPEC=massive-dml/generated-batched-update
```

Use profile diagnostics and workbench metrics to discuss stop criteria. Noisia
is optional background pressure supplied by the platform, not duplicated by
the profile.
