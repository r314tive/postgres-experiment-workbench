\set ON_ERROR_STOP on
\timing on

\echo 'replication-slots profile: generate WAL'
INSERT INTO replication_slots.events (tenant_id, payload)
SELECT
    (gs % 64)::integer,
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

UPDATE replication_slots.events
SET payload = payload || md5(id::text)
WHERE id % 7 = 0;

ANALYZE replication_slots.events;

\echo 'replication-slots profile: physical slot retention'
SELECT
    slot_name,
    slot_type,
    active,
    restart_lsn,
    confirmed_flush_lsn,
    pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained_wal
FROM pg_replication_slots
ORDER BY slot_name;

\echo 'replication-slots profile: streaming replication status'
SELECT
    application_name,
    state,
    sync_state,
    sent_lsn,
    replay_lsn,
    pg_size_pretty(pg_wal_lsn_diff(sent_lsn, replay_lsn)) AS replay_lag_bytes
FROM pg_stat_replication
ORDER BY application_name;
