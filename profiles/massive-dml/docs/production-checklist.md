# Production checklist

The workbench uses synthetic data. Production execution must use the real
database, predicate, indexes, workload, monitoring, and rollback requirements.

## Before

- Confirm the exact database, schema, table, predicate, and affected row count.
- Verify the batching key distribution and idempotency.
- For timestamp backfills, verify source units, target type, null semantics,
  and timezone behavior.
- Freeze a DELETE cutoff once; do not recalculate `now()` per batch.
- Confirm the selector's index with `pg_get_indexdef` and the exact plan.
- Measure one representative batch with `EXPLAIN (ANALYZE, BUFFERS)` inside
  `BEGIN ... ROLLBACK`.
- Choose batch size and sleep from measured cost.
- Set per-batch `lock_timeout` and `statement_timeout`.
- Estimate WAL, replica lag, disk headroom, dead tuples, and vacuum work.
- Review the generated SQL and confirm explicit transaction boundaries.
- Confirm the runner propagates the first `psql` error.
- Define stop criteria and rollback limits before the first commit.

## During

- Watch application latency, waits, blockers, WAL rate, replica lag, disk I/O,
  storage growth, batch duration, and affected row counts.
- Stop when measured impact exceeds the agreed threshold.
- If stopped, derive committed progress from database state and durable logs.

## After

- Verify remaining rows and sample transformed rows.
- Verify retained rows after DELETE.
- Inspect table statistics and dead tuples.
- Run or schedule `VACUUM (ANALYZE)` when appropriate.
- Avoid `VACUUM FULL` unless its lock and rewrite cost are explicitly accepted.
- Preserve the generated SQL, execution log, metrics, and final report.
