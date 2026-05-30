\set ON_ERROR_STOP on
\timing on

\echo 'vacuum-bloat profile: initial table stats'
SELECT
    relname,
    n_live_tup,
    n_dead_tup,
    vacuum_count,
    autovacuum_count,
    last_vacuum,
    last_autovacuum
FROM pg_stat_user_tables
WHERE schemaname = 'vacuum_bloat'
ORDER BY relname;

\echo 'vacuum-bloat profile: create dead tuples with committed churn'
UPDATE vacuum_bloat.events
SET
    status = 'review',
    updated_at = clock_timestamp()
WHERE id % 5 = 0;

DELETE FROM vacuum_bloat.events
WHERE id % 13 = 0;

ANALYZE vacuum_bloat.events;

\echo 'vacuum-bloat profile: stats after churn'
SELECT
    relname,
    n_live_tup,
    n_dead_tup,
    pg_size_pretty(pg_total_relation_size(relid)) AS total_size,
    pg_size_pretty(pg_relation_size(relid)) AS heap_size
FROM pg_stat_user_tables
WHERE schemaname = 'vacuum_bloat'
ORDER BY relname;

\echo 'vacuum-bloat profile: selective query after churn'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM vacuum_bloat.events
WHERE tenant_id = 42
  AND status IN ('open', 'review');

\echo 'vacuum-bloat profile: manual vacuum'
VACUUM (VERBOSE, ANALYZE) vacuum_bloat.events;

\echo 'vacuum-bloat profile: stats after vacuum'
SELECT
    relname,
    n_live_tup,
    n_dead_tup,
    vacuum_count,
    last_vacuum,
    pg_size_pretty(pg_total_relation_size(relid)) AS total_size
FROM pg_stat_user_tables
WHERE schemaname = 'vacuum_bloat'
ORDER BY relname;
