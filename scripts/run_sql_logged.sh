#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SQL_FILE="${1:?Usage: scripts/run_sql_logged.sh path/to/file.sql [log-file]}"
LOG_FILE="${2:-$REPO_DIR/logs/$(basename "$SQL_FILE").$(date +%Y%m%d_%H%M%S).log}"

mkdir -p "$(dirname "$LOG_FILE")"

{
  printf 'started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'sql_file=%s\n' "$SQL_FILE"
  printf 'log_file=%s\n' "$LOG_FILE"
  "$REPO_DIR/scripts/psql.sh" -v ON_ERROR_STOP=1 -f "$SQL_FILE"
  printf 'finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} 2>&1 | tee "$LOG_FILE"
