#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$REPO_DIR/logs/snapshots/$(date -u +%Y%m%d_%H%M%S)}"

mkdir -p "$OUT_DIR"

"$REPO_DIR/scripts/psql.sh" -f "$REPO_DIR/sql/monitor.sql" > "$OUT_DIR/monitor.txt"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -c "
SELECT name, setting, unit, source, pending_restart
FROM pg_settings
WHERE name IN (
  'shared_buffers',
  'work_mem',
  'maintenance_work_mem',
  'max_connections',
  'wal_level',
  'max_wal_size',
  'checkpoint_timeout',
  'autovacuum',
  'shared_preload_libraries',
  'log_min_duration_statement',
  'track_io_timing'
)
ORDER BY name;
" > "$OUT_DIR/settings.tsv"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -c "
SELECT extname, extversion
FROM pg_extension
ORDER BY extname;
" > "$OUT_DIR/extensions.tsv"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -c "
SELECT schemaname, relname, n_live_tup, n_dead_tup,
       vacuum_count, autovacuum_count, analyze_count, autoanalyze_count
FROM pg_stat_user_tables
ORDER BY schemaname, relname;
" > "$OUT_DIR/user_tables.tsv"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -c "
SELECT schemaname, relname, indexrelname, idx_scan, idx_tup_read, idx_tup_fetch
FROM pg_stat_user_indexes
ORDER BY schemaname, relname, indexrelname;
" > "$OUT_DIR/user_indexes.tsv"

if "$REPO_DIR/scripts/psql.sh" -A -t -c "SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements';" | grep -q '^1$'; then
  "$REPO_DIR/scripts/psql.sh" -A -F $'\t' -c "
SELECT calls, total_exec_time, rows, left(query, 300) AS query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 50;
" > "$OUT_DIR/pg_stat_statements.tsv"
fi

printf 'snapshot_dir=%s\n' "$OUT_DIR"
