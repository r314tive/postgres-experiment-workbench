#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MATRIX_LIST="$("$REPO_DIR/scripts/run_experiment_matrix.sh" list)"
grep -q '^smoke$' <<< "$MATRIX_LIST"

MATRIX_SHOW="$("$REPO_DIR/scripts/run_experiment_matrix.sh" show smoke)"
grep -q 'MATRIX_NAME="smoke matrix"' <<< "$MATRIX_SHOW"

MATRIX_PLAN="$("$REPO_DIR/scripts/run_experiment_matrix.sh" plan smoke)"
grep -q '# Experiment Matrix Plan' <<< "$MATRIX_PLAN"
grep -q '| `smoke` | `default` | `small` | `1` |' <<< "$MATRIX_PLAN"

MATRIX_RUN_ID="test-matrix-$(date -u +%Y%m%d_%H%M%S)"
MATRIX_RUN_ID="$MATRIX_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment_matrix.sh" run smoke >/dev/null

MATRIX_DIR="$REPO_DIR/runs/matrices/$MATRIX_RUN_ID"
test -s "$MATRIX_DIR/summary.md"
grep -q 'passed' "$MATRIX_DIR/runs.tsv"

echo "PASS: experiment matrices"
