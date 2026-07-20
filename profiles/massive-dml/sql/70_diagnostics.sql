\set ON_ERROR_STOP on
\pset pager off

\echo '== locks on massive-dml tables =='
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
WHERE l.relation IN (
    'massive_dml.transaction_log'::regclass,
    'massive_dml.audit_record'::regclass
)
ORDER BY l.granted, a.query_start NULLS LAST;

\echo '== massive-dml table stats =='
SELECT
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
WHERE schemaname = 'massive_dml'
ORDER BY relname;

\echo '== massive-dml progress =='
SELECT * FROM massive_dml.transaction_log_backfill_stats;
SELECT * FROM massive_dml.audit_record_delete_stats;
