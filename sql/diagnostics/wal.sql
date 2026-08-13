\pset null '[null]'
\echo 'diagnostic: wal'

SELECT
  clock_timestamp() AS sampled_at,
  pg_current_wal_lsn() AS current_wal_lsn,
  wal_records,
  wal_fpi,
  pg_size_pretty(wal_bytes) AS wal_bytes,
  wal_buffers_full,
  COALESCE((to_jsonb(w) ->> 'wal_write')::bigint, 0) AS wal_write,
  COALESCE((to_jsonb(w) ->> 'wal_sync')::bigint, 0) AS wal_sync,
  stats_reset
FROM pg_stat_wal AS w;

\echo 'diagnostic: checkpoints'

SELECT
  checkpoints_timed,
  checkpoints_req,
  checkpoint_write_time,
  checkpoint_sync_time,
  buffers_checkpoint,
  buffers_clean,
  maxwritten_clean,
  buffers_backend,
  buffers_backend_fsync,
  buffers_alloc,
  stats_reset
FROM pg_stat_bgwriter;
