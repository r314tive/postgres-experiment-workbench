\set ON_ERROR_STOP on
\timing on

\echo 'wal-pressure profile: capture starting WAL position'
SELECT pg_current_wal_lsn() AS wal_lsn_before \gset

\echo 'wal-pressure profile: logged insert phase'
INSERT INTO wal_pressure.events (id, tenant_id, event_type, payload)
SELECT
    gs,
    (gs % 100)::integer,
    CASE WHEN gs % 2 = 0 THEN 'click' ELSE 'view' END,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 4
        WHEN 'medium' THEN 8
        WHEN 'large' THEN 12
        ELSE 4
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 10000
        WHEN 'medium' THEN 50000
        WHEN 'large' THEN 200000
        ELSE 10000
    END
) AS gs;
SELECT pg_current_wal_lsn() AS wal_lsn_after_insert \gset

\echo 'wal-pressure profile: logged update phase'
UPDATE wal_pressure.events
SET
    event_type = event_type || '-updated',
    updated_at = clock_timestamp()
WHERE id % 3 = 0;
SELECT pg_current_wal_lsn() AS wal_lsn_after_update \gset

\echo 'wal-pressure profile: logged delete phase'
DELETE FROM wal_pressure.events
WHERE id % 5 = 0;
SELECT pg_current_wal_lsn() AS wal_lsn_after_delete \gset

ANALYZE wal_pressure.events;

\echo 'wal-pressure profile: WAL deltas by phase'
SELECT
    pg_size_pretty(pg_wal_lsn_diff(:'wal_lsn_after_insert', :'wal_lsn_before')) AS insert_wal,
    pg_size_pretty(pg_wal_lsn_diff(:'wal_lsn_after_update', :'wal_lsn_after_insert')) AS update_wal,
    pg_size_pretty(pg_wal_lsn_diff(:'wal_lsn_after_delete', :'wal_lsn_after_update')) AS delete_wal,
    pg_size_pretty(pg_wal_lsn_diff(:'wal_lsn_after_delete', :'wal_lsn_before')) AS total_wal;

\echo 'wal-pressure profile: current pg_stat_wal counters'
SELECT
    wal_records,
    wal_fpi,
    pg_size_pretty(wal_bytes) AS wal_bytes,
    wal_sync,
    wal_write
FROM pg_stat_wal;

\echo 'wal-pressure profile: relation size after writes'
SELECT
    pg_size_pretty(pg_relation_size('wal_pressure.events'::regclass)) AS heap_size,
    pg_size_pretty(pg_total_relation_size('wal_pressure.events'::regclass)) AS total_size,
    count(*) AS remaining_rows
FROM wal_pressure.events;
