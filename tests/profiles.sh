#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PROFILES=(
  locks
  vacuum-bloat
  indexes
  wal-pressure
  partitioning
  connection-pressure
)

for profile in "${PROFILES[@]}"; do
  PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" "$profile" 00_setup.sql >/dev/null
  PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" "$profile" 10_run.sql >/dev/null
  echo "PASS: $profile profile"
done

METRICS_SAMPLES=1 "$REPO_DIR/scripts/sample_metrics.sh" "$REPO_DIR/logs/test-metrics.csv" >/dev/null

if [[ "$(wc -l < "$REPO_DIR/logs/test-metrics.csv" | tr -d ' ')" -lt 2 ]]; then
  echo "FAIL: expected metrics sampler to write header and sample row" >&2
  exit 1
fi

echo "PASS: metrics sampler"
