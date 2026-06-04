#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PROFILES=(
  constraints
  locks
  vacuum-bloat
  indexes
  wal-pressure
  partitioning
  temp-spill
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

if grep -Eq 'Pager usage|Output format|Field separator' "$REPO_DIR/logs/test-metrics.csv"; then
  echo "FAIL: metrics sampler wrote psql formatting output into CSV" >&2
  exit 1
fi

if awk -F ',' 'NR == 1 { cols = NF; next } NF != cols { bad = 1 } END { exit bad }' "$REPO_DIR/logs/test-metrics.csv"; then
  :
else
  echo "FAIL: metrics sampler wrote rows with inconsistent column counts" >&2
  exit 1
fi

echo "PASS: metrics sampler"
