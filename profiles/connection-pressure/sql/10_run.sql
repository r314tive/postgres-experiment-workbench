\set ON_ERROR_STOP on
\timing on

\echo 'connection-pressure profile: bounded write/read activity'
INSERT INTO connection_pressure.events (client_tag, backend_pid, payload)
SELECT
    'client-' || (gs % 32),
    pg_backend_pid(),
    md5(gs::text)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 2000
        WHEN 'medium' THEN 10000
        WHEN 'large' THEN 50000
        ELSE 2000
    END
) AS gs;

ANALYZE connection_pressure.events;

SELECT
    count(*) AS rows_written,
    count(DISTINCT backend_pid) AS observed_backend_pids,
    min(created_at) AS first_seen,
    max(created_at) AS last_seen
FROM connection_pressure.events;

SELECT
    datname,
    state,
    wait_event_type,
    count(*) AS sessions
FROM pg_stat_activity
WHERE datname = current_database()
GROUP BY datname, state, wait_event_type
ORDER BY sessions DESC, state NULLS LAST;
