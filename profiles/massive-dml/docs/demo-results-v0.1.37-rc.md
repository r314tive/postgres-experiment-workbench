# v0.1.37 release-candidate demo results

This is a reproducibility record for the candidate implementation, not a
production capacity forecast.

## Environment

- Date: 2026-07-18
- Source state: the `v0.1.37` candidate tree based on `40781ad`
- Host: Apple silicon, macOS 26.5.2
- Runtime: Docker Compose, `postgres:16-alpine`
- PostgreSQL: 16.14, aarch64
- Matrix: `massive-dml-strategy`, `default` PG config, `medium` profile, three
  repeats per experiment
- Matrix run: `massive-dml-strategy-matrix-20260718_115024`

All 9 runs passed their SQL and shell assertions, artifact scan, verdict, and
run verification.

## Observed averages

| Scenario | Variant | Rows | Average total ms | Average WAL bytes |
| --- | --- | ---: | ---: | ---: |
| Offline bulk load | indexes maintained during load | 200,000 | 607.342 | 73,372,883 |
| Offline bulk load | secondary indexes built after load | 200,000 | 398.683 | 52,154,547 |
| Remove old partition data | row DELETE | 80,000 | 17.086 | 5,776,968 |
| Remove old partition data | detach/drop partition | 80,000 | 0.942 | 10,912 |

On this disposable local runtime, building secondary indexes after the load had
lower total time and WAL than maintaining them row by row. Detach/drop also
changed the removal from a row-level rewrite into a metadata-oriented partition
operation. These results demonstrate the decision shape; production choices
still require real schema, indexes, retention boundaries, concurrency, disk,
replication, and recovery constraints.

## Reproduce

```bash
make check
MATRIX_PROFILE_SIZES=medium MATRIX_REPEATS=3 \
  make matrix-run MATRIX_SPEC=massive-dml-strategy
make experiment-summary SUMMARY_INPUT=runs/matrices/<matrix-run-id>
```

Inspect `summary.md`, `statistics.md`, every run's `verdict.json`, and the
`bulk-load-*.tsv` or `partition-drop-vs-delete.tsv` artifact before presenting
the result.
