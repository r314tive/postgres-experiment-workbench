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
  METRICS_READY_FILE=

Set METRICS_SAMPLES=1 for a single sample. Without METRICS_SAMPLES, the
sampler runs until METRICS_DURATION seconds have elapsed. When an absolute
METRICS_READY_FILE is supplied, it is published exclusively as an empty
mode-0700 directory after the first successful sample. It is unset for
standalone use.
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
READY_FILE="${METRICS_READY_FILE:-}"

require_positive_int METRICS_INTERVAL "$INTERVAL"
require_nonnegative_int METRICS_DURATION "$DURATION"
if [[ -n "$SAMPLES" ]]; then
  require_positive_int METRICS_SAMPLES "$SAMPLES"
fi
if [[ -n "$READY_FILE" ]]; then
  if [[ "$READY_FILE" != /* ]]; then
    echo "METRICS_READY_FILE must be absolute, got: $READY_FILE" >&2
    exit 2
  fi
  if [[ -e "$READY_FILE" || -L "$READY_FILE" ]]; then
    echo "Refusing to overwrite metrics readiness token: $READY_FILE" >&2
    exit 2
  fi
  if [[ ! -d "$(dirname "$READY_FILE")" || -L "$(dirname "$READY_FILE")" ]]; then
    echo "METRICS_READY_FILE parent must be an existing non-symlink directory: $(dirname "$READY_FILE")" >&2
    exit 2
  fi
fi

mkdir -p "$(dirname "$OUT_FILE")"

HEADER="sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_returned,tup_fetched,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes,current_wal_lsn"

if [[ "${METRICS_APPEND:-0}" != "1" || ! -s "$OUT_FILE" ]]; then
  printf '%s\n' "$HEADER" > "$OUT_FILE"
fi

sample_once() {
  "$REPO_DIR/scripts/psql.sh" -q -f "$REPO_DIR/sql/metrics_sample.sql" >> "$OUT_FILE"
}

READY_PUBLISHED=0

publish_ready() {
  if [[ -z "$READY_FILE" || "$READY_PUBLISHED" = "1" ]]; then
    return 0
  fi
  # Plain mkdir under umask 077 publishes mode 0700 in the creation syscall and
  # cannot follow a raced symlink or block on a raced FIFO. Do not use `mkdir
  # -m`: BSD mkdir may apply that mode with a path-based chmod after creation;
  # the consumer can rmdir the visible token first and turn a valid publication
  # into a false producer failure.
  if ! (umask 077; mkdir -- "$READY_FILE") 2>/dev/null; then
    echo "Refusing to overwrite metrics readiness token: $READY_FILE" >&2
    return 1
  fi
  READY_PUBLISHED=1
}

sample_and_publish_ready() {
  sample_once
  publish_ready
}

# Duration-mode collectors are stopped explicitly after the foreground
# workload. Record one final boundary sample before exiting so a verifier can
# prove that metrics span the complete benchmark measure interval. A failed
# boundary sample remains a collector failure rather than silently claiming
# coverage.
trap 'sample_and_publish_ready; exit 0' TERM

if [[ -n "$SAMPLES" ]]; then
  for ((i = 1; i <= SAMPLES; i++)); do
    sample_and_publish_ready
    if (( i < SAMPLES )); then
      sleep "$INTERVAL"
    fi
  done
else
  START="$(date +%s)"
  END=$((START + DURATION))

  while true; do
    sample_and_publish_ready
    NOW="$(date +%s)"
    if (( NOW >= END )); then
      break
    fi
    sleep "$INTERVAL"
  done
fi

echo "Wrote metrics: $OUT_FILE"
