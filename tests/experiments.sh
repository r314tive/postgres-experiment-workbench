#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

EXPERIMENT_LIST="$("$REPO_DIR/scripts/run_experiment.sh" list)"
grep -q '^smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^locks-under-contention$' <<< "$EXPERIMENT_LIST"

"$REPO_DIR/scripts/run_experiment.sh" show smoke | grep -q 'EXPERIMENT_NAME="smoke experiment"'

RUN_ID="test-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run smoke >/dev/null

RUN_DIR="$REPO_DIR/runs/$RUN_ID"
if [[ ! -f "$RUN_DIR/verdict.json" ]]; then
  echo "FAIL: expected verdict.json in $RUN_DIR" >&2
  exit 1
fi

grep -q '"status": "passed"' "$RUN_DIR/verdict.json"
test -s "$RUN_DIR/manifest.env"
test -s "$RUN_DIR/metrics.csv"

REPEAT_ID="test-repeat-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_REPEAT_ID="$REPEAT_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment_repeated.sh" smoke 1 >/dev/null

REPEAT_DIR="$REPO_DIR/runs/repeats/$REPEAT_ID"
test -s "$REPEAT_DIR/summary.md"
test -s "$REPEAT_DIR/statistics.md"
grep -q 'passed' "$REPEAT_DIR/runs.tsv"
grep -q '# Run Series Summary' "$REPEAT_DIR/statistics.md"

echo "PASS: experiments"
