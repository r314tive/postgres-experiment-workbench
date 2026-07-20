\set ON_ERROR_STOP on

SET client_min_messages = warning;
SET TimeZone = 'UTC';

SELECT
    CASE :'profile_size'
        WHEN 'small' THEN 5000
        WHEN 'medium' THEN 50000
        WHEN 'large' THEN 1000000
        ELSE 5000
    END AS transaction_rows,
    CASE :'profile_size'
        WHEN 'small' THEN 3000
        WHEN 'medium' THEN 30000
        WHEN 'large' THEN 500000
        ELSE 3000
    END AS audit_rows,
    CASE :'profile_size'
        WHEN 'small' THEN 1200
        WHEN 'medium' THEN 12000
        WHEN 'large' THEN 250000
        ELSE 1200
    END AS old_audit_rows,
    CASE :'profile_size'
        WHEN 'small' THEN 50
        WHEN 'medium' THEN 100
        WHEN 'large' THEN 200
        ELSE 50
    END AS transaction_payload_bytes,
    CASE :'profile_size'
        WHEN 'small' THEN 60
        WHEN 'medium' THEN 120
        WHEN 'large' THEN 200
        ELSE 60
    END AS audit_payload_bytes
\gset

DROP SCHEMA IF EXISTS massive_dml CASCADE;
CREATE SCHEMA massive_dml;

CREATE TABLE massive_dml.experiment_results (
    scenario text NOT NULL,
    variant text NOT NULL,
    rows_affected bigint NOT NULL,
    load_ms numeric,
    index_ms numeric,
    total_ms numeric NOT NULL,
    wal_bytes numeric NOT NULL,
    table_bytes bigint NOT NULL,
    index_bytes bigint NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (scenario, variant)
);

CREATE TABLE massive_dml.transaction_log (
    id bigint PRIMARY KEY,
    created_at bigint,
    processed_at bigint,
    created_ts timestamptz,
    processed_ts timestamptz,
    payload text NOT NULL
);

WITH src AS (
    SELECT
        gs AS id,
        TIMESTAMPTZ '2026-01-01 00:00:00+00' + make_interval(secs => gs) AS created_time,
        TIMESTAMPTZ '2026-01-01 00:00:00+00' + make_interval(secs => gs + 60) AS processed_time
    FROM generate_series(1, :transaction_rows::bigint) AS gs
),
prepared AS (
    SELECT
        id,
        CASE
            WHEN id % 17 = 0 THEN NULL
            ELSE (extract(epoch FROM created_time) * 1000000)::bigint
        END AS created_at,
        CASE
            WHEN id % 19 = 0 THEN NULL
            ELSE (extract(epoch FROM processed_time) * 1000000)::bigint
        END AS processed_at,
        created_time,
        processed_time
    FROM src
)
INSERT INTO massive_dml.transaction_log (
    id,
    created_at,
    processed_at,
    created_ts,
    processed_ts,
    payload
)
SELECT
    id,
    created_at,
    processed_at,
    CASE
        WHEN created_at IS NOT NULL AND id % 2 <> 0 THEN created_time
        ELSE NULL
    END AS created_ts,
    CASE
        WHEN processed_at IS NOT NULL AND id % 3 <> 0 THEN processed_time
        ELSE NULL
    END AS processed_ts,
    repeat('x', :transaction_payload_bytes::integer) AS payload
FROM prepared;

CREATE TABLE massive_dml.audit_record (
    audit_record_id bigint PRIMARY KEY,
    created_at timestamptz NOT NULL,
    payload text NOT NULL
);

INSERT INTO massive_dml.audit_record (audit_record_id, created_at, payload)
SELECT
    gs AS audit_record_id,
    CASE
        WHEN gs <= LEAST(:old_audit_rows::bigint, :audit_rows::bigint)
        THEN TIMESTAMPTZ '2026-03-01 00:00:00+00' + make_interval(secs => gs)
        ELSE TIMESTAMPTZ '2026-04-15 00:00:00+00' + make_interval(secs => gs)
    END AS created_at,
    repeat('a', :audit_payload_bytes::integer) AS payload
FROM generate_series(1, :audit_rows::bigint) AS gs;

CREATE INDEX audit_record_created_at_audit_record_id_idx
ON massive_dml.audit_record (created_at, audit_record_id);

CREATE VIEW massive_dml.transaction_log_backfill_stats AS
SELECT
    count(*) AS total_rows,
    count(*) FILTER (
        WHERE (created_ts IS NULL AND created_at IS NOT NULL)
           OR (processed_ts IS NULL AND processed_at IS NOT NULL)
    ) AS backfillable_remaining,
    count(*) FILTER (
        WHERE created_at IS NULL AND created_ts IS NULL
    ) AS created_source_null_rows,
    count(*) FILTER (
        WHERE processed_at IS NULL AND processed_ts IS NULL
    ) AS processed_source_null_rows,
    count(*) FILTER (
        WHERE created_at IS NULL AND created_ts IS NOT NULL
    ) AS invalid_created_backfills,
    count(*) FILTER (
        WHERE processed_at IS NULL AND processed_ts IS NOT NULL
    ) AS invalid_processed_backfills
FROM massive_dml.transaction_log;

CREATE VIEW massive_dml.audit_record_delete_stats AS
SELECT
    count(*) AS total_rows,
    count(*) FILTER (
        WHERE created_at < TIMESTAMPTZ '2026-04-07 00:00:00+00'
    ) AS old_rows
FROM massive_dml.audit_record;

ANALYZE massive_dml.transaction_log;
ANALYZE massive_dml.audit_record;

\echo 'massive-dml profile prepared'
SELECT :'profile_size' AS profile_size, *
FROM massive_dml.transaction_log_backfill_stats;
SELECT :'profile_size' AS profile_size, *
FROM massive_dml.audit_record_delete_stats;
