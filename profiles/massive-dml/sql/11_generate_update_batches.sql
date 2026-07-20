\set ON_ERROR_STOP on

\if :{?batch_size}
\else
\set batch_size 5000
\endif

\if :{?sleep_seconds}
\else
\set sleep_seconds 0
\endif

WITH settings AS (
    SELECT
        :batch_size::bigint AS batch_size,
        :sleep_seconds::numeric AS sleep_seconds
),
bounds AS (
    SELECT
        min(id) AS min_id,
        max(id) AS max_id
    FROM massive_dml.transaction_log
    WHERE (created_ts IS NULL AND created_at IS NOT NULL)
       OR (processed_ts IS NULL AND processed_at IS NOT NULL)
),
batches AS (
    SELECT
        row_number() OVER (ORDER BY gs) AS batch_no,
        gs AS from_id,
        LEAST(gs + s.batch_size - 1, b.max_id) AS to_id,
        s.sleep_seconds
    FROM bounds b
    CROSS JOIN settings s
    CROSS JOIN LATERAL generate_series(b.min_id, b.max_id, s.batch_size) AS gs
    WHERE b.min_id IS NOT NULL
),
rendered AS (
    SELECT
        0::bigint AS ord,
        $header$\set ON_ERROR_STOP on
\timing on
\echo 'generated UPDATE batches: massive_dml.transaction_log timestamp backfill'

$header$ AS sql
    UNION ALL
    SELECT
        batch_no AS ord,
        format($fmt$
\echo 'batch %1$s: id %2$s..%3$s'
BEGIN;

SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '10min';
SET LOCAL TimeZone = 'UTC';

UPDATE massive_dml.transaction_log
SET created_ts = CASE
        WHEN created_ts IS NULL AND created_at IS NOT NULL
        THEN to_timestamp(created_at / 1000000.0)
        ELSE created_ts
    END,
    processed_ts = CASE
        WHEN processed_ts IS NULL AND processed_at IS NOT NULL
        THEN to_timestamp(processed_at / 1000000.0)
        ELSE processed_ts
    END
WHERE id BETWEEN %2$s AND %3$s
  AND (
        (created_ts IS NULL AND created_at IS NOT NULL)
     OR (processed_ts IS NULL AND processed_at IS NOT NULL)
  );

COMMIT;

SELECT pg_sleep(%4$L);

$fmt$, batch_no, from_id, to_id, sleep_seconds) AS sql
    FROM batches
)
SELECT sql
FROM rendered
ORDER BY ord;
