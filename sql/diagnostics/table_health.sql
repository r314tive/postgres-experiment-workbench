\pset null '[null]'
\echo 'diagnostic: table health'

SELECT
  schemaname,
  relname,
  n_live_tup,
  n_dead_tup,
  round(100.0 * n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1), 2) AS dead_pct,
  n_mod_since_analyze,
  vacuum_count,
  autovacuum_count,
  analyze_count,
  autoanalyze_count,
  last_vacuum,
  last_autovacuum,
  last_analyze,
  last_autoanalyze,
  pg_size_pretty(pg_total_relation_size(format('%I.%I', schemaname, relname)::regclass)) AS total_size
FROM pg_stat_user_tables
ORDER BY
  n_dead_tup DESC,
  n_mod_since_analyze DESC,
  pg_total_relation_size(format('%I.%I', schemaname, relname)::regclass) DESC
LIMIT 100;
