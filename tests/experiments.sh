#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

EXPERIMENT_LIST="$("$REPO_DIR/scripts/run_experiment.sh" list)"
grep -q '^smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^locks-under-contention$' <<< "$EXPERIMENT_LIST"
grep -q '^replica-readonly$' <<< "$EXPERIMENT_LIST"
grep -q '^logical-replication$' <<< "$EXPERIMENT_LIST"
grep -q '^pgbouncer-smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^multi-version-upgrade-smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^temp-spill$' <<< "$EXPERIMENT_LIST"

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

REPLICA_RUN_ID="test-replica-readonly-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$REPLICA_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run replica-readonly >/dev/null

REPLICA_RUN_DIR="$REPO_DIR/runs/$REPLICA_RUN_ID"
grep -q '"status": "passed"' "$REPLICA_RUN_DIR/verdict.json"
grep -q 'experiment_topology=primary-replica' "$REPLICA_RUN_DIR/manifest.env"

LOGICAL_RUN_ID="test-logical-replication-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$LOGICAL_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run logical-replication >/dev/null

LOGICAL_RUN_DIR="$REPO_DIR/runs/$LOGICAL_RUN_ID"
grep -q '"status": "passed"' "$LOGICAL_RUN_DIR/verdict.json"
grep -q 'experiment_topology=logical-replication' "$LOGICAL_RUN_DIR/manifest.env"

PGBOUNCER_RUN_ID="test-pgbouncer-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$PGBOUNCER_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run pgbouncer-smoke >/dev/null

PGBOUNCER_RUN_DIR="$REPO_DIR/runs/$PGBOUNCER_RUN_ID"
grep -q '"status": "passed"' "$PGBOUNCER_RUN_DIR/verdict.json"
grep -q 'experiment_topology=pgbouncer' "$PGBOUNCER_RUN_DIR/manifest.env"

UPGRADE_RUN_ID="test-multi-version-upgrade-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$UPGRADE_RUN_ID" \
  "$REPO_DIR/scripts/run_experiment.sh" run multi-version-upgrade-smoke >/dev/null

UPGRADE_RUN_DIR="$REPO_DIR/runs/$UPGRADE_RUN_ID"
grep -q '"status": "passed"' "$UPGRADE_RUN_DIR/verdict.json"
grep -q 'experiment_topology=multi-version-upgrade' "$UPGRADE_RUN_DIR/manifest.env"

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
