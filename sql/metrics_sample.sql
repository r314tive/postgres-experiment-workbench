\set ON_ERROR_STOP on
\pset pager off
\pset format unaligned
\pset tuples_only on
\pset fieldsep ','

WITH database_oid AS (
    SELECT oid
    FROM pg_database
    WHERE datname = current_database()
),
activity AS (
    SELECT
        count(*) FILTER (WHERE state = 'active') AS active_sessions,
        count(*) FILTER (WHERE wait_event_type IS NOT NULL) AS waiting_sessions,
        count(*) FILTER (WHERE wait_event_type = 'Lock') AS lock_waiting_sessions,
        count(*) FILTER (WHERE cardinality(pg_blocking_pids(pid)) > 0) AS blocked_sessions
    FROM pg_stat_activity
    WHERE datname = current_database()
),
lock_summary AS (
    SELECT
        count(*) AS locks_total,
        count(*) FILTER (WHERE NOT granted) AS locks_waiting
    FROM pg_locks
    WHERE database = (SELECT oid FROM database_oid)
),
database_stats AS (
    SELECT
        xact_commit,
        xact_rollback,
        blks_read,
        blks_hit,
        tup_returned,
        tup_fetched,
        tup_inserted,
        tup_updated,
        tup_deleted,
        conflicts,
        deadlocks,
        temp_files,
        temp_bytes
    FROM pg_stat_database
    WHERE datname = current_database()
),
wal_stats AS (
    SELECT
        wal_records,
        wal_fpi,
        wal_bytes
    FROM pg_stat_wal
)
SELECT
    to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') AS sampled_at,
    current_database() AS database_name,
    activity.active_sessions,
    activity.waiting_sessions,
    activity.lock_waiting_sessions,
    activity.blocked_sessions,
    lock_summary.locks_total,
    lock_summary.locks_waiting,
    database_stats.xact_commit,
    database_stats.xact_rollback,
    database_stats.blks_read,
    database_stats.blks_hit,
    database_stats.tup_returned,
    database_stats.tup_fetched,
    database_stats.tup_inserted,
    database_stats.tup_updated,
    database_stats.tup_deleted,
    database_stats.conflicts,
    database_stats.deadlocks,
    database_stats.temp_files,
    database_stats.temp_bytes,
    wal_stats.wal_records,
    wal_stats.wal_fpi,
    wal_stats.wal_bytes,
    pg_current_wal_lsn() AS current_wal_lsn
FROM activity
CROSS JOIN lock_summary
CROSS JOIN database_stats
CROSS JOIN wal_stats;
