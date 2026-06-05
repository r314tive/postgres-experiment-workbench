\pset null '[null]'
\echo 'diagnostic: index health'

SELECT
  index_stats.schemaname,
  index_stats.relname,
  index_stats.indexrelname,
  index_stats.idx_scan,
  index_stats.idx_tup_read,
  index_stats.idx_tup_fetch,
  table_stats.seq_scan,
  table_stats.n_tup_ins + table_stats.n_tup_upd + table_stats.n_tup_del AS table_writes,
  pg_size_pretty(pg_relation_size(index_stats.indexrelid)) AS index_size,
  CASE
    WHEN index_stats.idx_scan = 0 AND pg_relation_size(index_stats.indexrelid) > 1024 * 1024 THEN 'large_never_scanned'
    WHEN index_stats.idx_scan < 10 AND table_stats.seq_scan > 100 THEN 'low_scan_with_table_seq_activity'
    ELSE 'observe'
  END AS signal
FROM pg_stat_user_indexes AS index_stats
JOIN pg_stat_user_tables AS table_stats
  ON table_stats.relid = index_stats.relid
ORDER BY
  CASE
    WHEN index_stats.idx_scan = 0 THEN 0
    ELSE 1
  END,
  pg_relation_size(index_stats.indexrelid) DESC,
  index_stats.idx_scan
LIMIT 100;
