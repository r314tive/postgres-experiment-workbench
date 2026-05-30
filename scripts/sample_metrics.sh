#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/sample_metrics.sh [output.csv]

Environment:
  METRICS_INTERVAL=1
  METRICS_DURATION=30
  METRICS_SAMPLES=
  METRICS_OUT=logs/metrics/metrics.<timestamp>.csv
  METRICS_APPEND=0

Set METRICS_SAMPLES=1 for a single sample. Without METRICS_SAMPLES, the
sampler runs until METRICS_DURATION seconds have elapsed.
USAGE
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

require_positive_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "$label must be a positive integer, got: $value" >&2
    exit 2
  fi
}

require_nonnegative_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[0-9]+$ ]]; then
    echo "$label must be a non-negative integer, got: $value" >&2
    exit 2
  fi
}

INTERVAL="${METRICS_INTERVAL:-1}"
DURATION="${METRICS_DURATION:-30}"
SAMPLES="${METRICS_SAMPLES:-}"
OUT_FILE="${1:-${METRICS_OUT:-$REPO_DIR/logs/metrics/metrics.$(timestamp).csv}}"

require_positive_int METRICS_INTERVAL "$INTERVAL"
require_nonnegative_int METRICS_DURATION "$DURATION"
if [[ -n "$SAMPLES" ]]; then
  require_positive_int METRICS_SAMPLES "$SAMPLES"
fi

mkdir -p "$(dirname "$OUT_FILE")"

HEADER="sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_returned,tup_fetched,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes,current_wal_lsn"

if [[ "${METRICS_APPEND:-0}" != "1" || ! -s "$OUT_FILE" ]]; then
  printf '%s\n' "$HEADER" > "$OUT_FILE"
fi

sample_once() {
  "$REPO_DIR/scripts/psql.sh" -A -t -F ',' -f "$REPO_DIR/sql/metrics_sample.sql" >> "$OUT_FILE"
}

if [[ -n "$SAMPLES" ]]; then
  for ((i = 1; i <= SAMPLES; i++)); do
    sample_once
    if (( i < SAMPLES )); then
      sleep "$INTERVAL"
    fi
  done
else
  START="$(date +%s)"
  END=$((START + DURATION))

  while true; do
    sample_once
    NOW="$(date +%s)"
    if (( NOW >= END )); then
      break
    fi
    sleep "$INTERVAL"
  done
fi

echo "Wrote metrics: $OUT_FILE"
