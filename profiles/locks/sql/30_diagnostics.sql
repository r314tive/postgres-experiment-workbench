\set ON_ERROR_STOP on
\pset pager off

\echo 'locks profile: sessions with blocking context'
SELECT
    pid,
    application_name,
    state,
    wait_event_type,
    wait_event,
    pg_blocking_pids(pid) AS blocking_pids,
    now() - query_start AS duration,
    left(query, 160) AS query
FROM pg_stat_activity
WHERE datname = current_database()
  AND (wait_event_type = 'Lock' OR cardinality(pg_blocking_pids(pid)) > 0)
ORDER BY query_start NULLS LAST;

\echo 'locks profile: ungranted locks'
SELECT
    l.pid,
    a.application_name,
    l.locktype,
    l.mode,
    l.granted,
    l.relation::regclass AS relation,
    pg_blocking_pids(l.pid) AS blocking_pids
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid = l.pid
WHERE NOT l.granted
ORDER BY l.pid, l.locktype, l.mode;
