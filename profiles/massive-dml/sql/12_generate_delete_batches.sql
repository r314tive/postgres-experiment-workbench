\set ON_ERROR_STOP on

\if :{?batch_size}
\else
\set batch_size 5000
\endif

\if :{?sleep_seconds}
\else
\set sleep_seconds 0
\endif

\if :{?cutoff}
\else
\set cutoff '''2026-04-07 00:00:00+00'''
\endif

WITH settings AS (
    SELECT
        :batch_size::bigint AS batch_size,
        :sleep_seconds::numeric AS sleep_seconds,
        :cutoff::timestamptz AS cutoff
),
batches AS (
    SELECT
        row_number() OVER (ORDER BY gs) AS batch_no,
        s.batch_size,
        s.sleep_seconds,
        s.cutoff
    FROM settings s
    CROSS JOIN LATERAL generate_series(
        0,
        GREATEST(
            (
                SELECT count(*)
                FROM massive_dml.audit_record
                WHERE created_at < s.cutoff
            ) - 1,
            0
        ),
        s.batch_size
    ) AS gs
),
rendered AS (
    SELECT
        0::bigint AS ord,
        $header$\set ON_ERROR_STOP on
\timing on
\echo 'generated DELETE batches: massive_dml.audit_record old rows'

$header$ AS sql
    UNION ALL
    SELECT
        batch_no AS ord,
        format($fmt$
\echo 'delete batch %1$s: limit %2$s'
BEGIN;

SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '10min';

WITH target AS (
    SELECT audit_record_id
    FROM massive_dml.audit_record
    WHERE created_at < %3$L::timestamptz
    ORDER BY created_at, audit_record_id
    LIMIT %2$s
    FOR UPDATE SKIP LOCKED
)
DELETE FROM massive_dml.audit_record ar
USING target t
WHERE ar.audit_record_id = t.audit_record_id;

COMMIT;

SELECT pg_sleep(%4$L);

$fmt$, batch_no, batch_size, cutoff, sleep_seconds) AS sql
    FROM batches
)
SELECT sql
FROM rendered
ORDER BY ord;
