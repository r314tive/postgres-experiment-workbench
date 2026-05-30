\set ON_ERROR_STOP on
\pset pager off

\echo '== active sessions =='
SELECT
    pid,
    usename,
    application_name,
    client_addr,
    now() - query_start AS duration,
    wait_event_type,
    wait_event,
    state,
    left(query, 160) AS query
FROM pg_stat_activity
WHERE datname = current_database()
  AND state <> 'idle'
ORDER BY query_start NULLS LAST;

\echo '== blocking sessions =='
SELECT
    blocked.pid AS blocked_pid,
    blocked.application_name AS blocked_application,
    blocked.wait_event_type,
    blocked.wait_event,
    pg_blocking_pids(blocked.pid) AS blocking_pids,
    now() - blocked.query_start AS blocked_duration,
    left(blocked.query, 120) AS blocked_query
FROM pg_stat_activity blocked
WHERE blocked.datname = current_database()
  AND cardinality(pg_blocking_pids(blocked.pid)) > 0
ORDER BY blocked.query_start NULLS LAST;

\echo '== user tables =='
SELECT
    schemaname,
    relname,
    n_live_tup,
    n_dead_tup,
    vacuum_count,
    autovacuum_count,
    analyze_count,
    autoanalyze_count,
    last_vacuum,
    last_autovacuum,
    last_analyze,
    last_autoanalyze
FROM pg_stat_user_tables
ORDER BY schemaname, relname;

\echo '== relation locks =='
SELECT
    a.pid,
    a.application_name,
    now() - a.query_start AS duration,
    a.wait_event_type,
    a.wait_event,
    l.mode,
    l.granted,
    l.relation::regclass AS relation,
    left(a.query, 120) AS query
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid = l.pid
WHERE l.database = (
    SELECT oid
    FROM pg_database
    WHERE datname = current_database()
)
  AND l.relation IS NOT NULL
ORDER BY l.granted, a.query_start NULLS LAST, relation::text;

\echo '== database counters =='
SELECT
    datname,
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
    pg_size_pretty(temp_bytes) AS temp_bytes
FROM pg_stat_database
WHERE datname = current_database();

\echo '== wal counters =='
SELECT
    wal_records,
    wal_fpi,
    pg_size_pretty(wal_bytes) AS wal_bytes,
    wal_write,
    wal_sync
FROM pg_stat_wal;
