\set ON_ERROR_STOP on

SELECT CASE :'profile_size'
    WHEN 'small' THEN 500
    WHEN 'medium' THEN 5000
    WHEN 'large' THEN 10000
    ELSE 500
END AS massive_dml_explain_to_id
\gset

BEGIN;

SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '10min';
SET LOCAL TimeZone = 'UTC';

EXPLAIN (ANALYZE, BUFFERS)
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
WHERE id BETWEEN 1 AND :massive_dml_explain_to_id
  AND (
        (created_ts IS NULL AND created_at IS NOT NULL)
     OR (processed_ts IS NULL AND processed_at IS NOT NULL)
  );

ROLLBACK;
