#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

manifest_value() {
  local manifest_file="$1"
  local key="$2"
  local value
  value="$(awk -F= -v key="$key" '$1 == key { print substr($0, length(key) + 2); exit }' "$manifest_file")"
  if [[ "$value" == '"*"' ]]; then
    value="${value:1:${#value}-2}"
  fi
  printf '%s' "$value"
}

EXPERIMENT_LIST="$("$REPO_DIR/scripts/run_experiment.sh" list)"
grep -q '^smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^constraints-validation$' <<< "$EXPERIMENT_LIST"
grep -q '^jsonb-indexing$' <<< "$EXPERIMENT_LIST"
grep -q '^locks-under-contention$' <<< "$EXPERIMENT_LIST"
grep -q '^replica-readonly$' <<< "$EXPERIMENT_LIST"
grep -q '^logical-replication$' <<< "$EXPERIMENT_LIST"
grep -q '^logical-ddl$' <<< "$EXPERIMENT_LIST"
grep -q '^pgbouncer-smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^multi-version-upgrade-smoke$' <<< "$EXPERIMENT_LIST"
grep -q '^temp-spill$' <<< "$EXPERIMENT_LIST"

"$REPO_DIR/scripts/run_experiment.sh" show smoke | grep -q 'EXPERIMENT_NAME="smoke experiment"'

RUN_ID="test-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$RUN_ID" \
EXPERIMENT_STATE_WRITER=go \
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

SHELL_WRITER_RUN_ID="test-smoke-shell-writer-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$SHELL_WRITER_RUN_ID" \
EXPERIMENT_STATE_WRITER=shell \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run smoke >/dev/null

SHELL_WRITER_RUN_DIR="$REPO_DIR/runs/$SHELL_WRITER_RUN_ID"
grep -q '"status": "passed"' "$SHELL_WRITER_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$SHELL_WRITER_RUN_DIR/manifest.env" experiment_topology)" != "single" ]]; then
  echo "FAIL: expected experiment_topology=single in manifest" >&2
  exit 1
fi

CONSTRAINTS_RUN_ID="test-constraints-validation-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$CONSTRAINTS_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run constraints-validation >/dev/null

CONSTRAINTS_RUN_DIR="$REPO_DIR/runs/$CONSTRAINTS_RUN_ID"
grep -q '"status": "passed"' "$CONSTRAINTS_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$CONSTRAINTS_RUN_DIR/manifest.env" profile)" != "constraints" ]]; then
  echo "FAIL: expected profile=constraints in manifest" >&2
  exit 1
fi

JSONB_RUN_ID="test-jsonb-indexing-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$JSONB_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run jsonb-indexing >/dev/null

JSONB_RUN_DIR="$REPO_DIR/runs/$JSONB_RUN_ID"
grep -q '"status": "passed"' "$JSONB_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$JSONB_RUN_DIR/manifest.env" profile)" != "jsonb" ]]; then
  echo "FAIL: expected profile=jsonb in manifest" >&2
  exit 1
fi

REPLICA_RUN_ID="test-replica-readonly-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$REPLICA_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run replica-readonly >/dev/null

REPLICA_RUN_DIR="$REPO_DIR/runs/$REPLICA_RUN_ID"
grep -q '"status": "passed"' "$REPLICA_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$REPLICA_RUN_DIR/manifest.env" experiment_topology)" != "primary-replica" ]]; then
  echo "FAIL: expected experiment_topology=primary-replica in manifest" >&2
  exit 1
fi

LOGICAL_RUN_ID="test-logical-replication-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$LOGICAL_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run logical-replication >/dev/null

LOGICAL_RUN_DIR="$REPO_DIR/runs/$LOGICAL_RUN_ID"
grep -q '"status": "passed"' "$LOGICAL_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$LOGICAL_RUN_DIR/manifest.env" experiment_topology)" != "logical-replication" ]]; then
  echo "FAIL: expected experiment_topology=logical-replication in manifest" >&2
  exit 1
fi

LOGICAL_DDL_RUN_ID="test-logical-ddl-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$LOGICAL_DDL_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run logical-ddl >/dev/null

LOGICAL_DDL_RUN_DIR="$REPO_DIR/runs/$LOGICAL_DDL_RUN_ID"
grep -q '"status": "passed"' "$LOGICAL_DDL_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$LOGICAL_DDL_RUN_DIR/manifest.env" profile)" != "logical-ddl" ]]; then
  echo "FAIL: expected profile=logical-ddl in manifest" >&2
  exit 1
fi

PGBOUNCER_RUN_ID="test-pgbouncer-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$PGBOUNCER_RUN_ID" \
EXPERIMENT_SNAPSHOT=0 \
EXPERIMENT_METRICS_SAMPLES=1 \
  "$REPO_DIR/scripts/run_experiment.sh" run pgbouncer-smoke >/dev/null

PGBOUNCER_RUN_DIR="$REPO_DIR/runs/$PGBOUNCER_RUN_ID"
grep -q '"status": "passed"' "$PGBOUNCER_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$PGBOUNCER_RUN_DIR/manifest.env" experiment_topology)" != "pgbouncer" ]]; then
  echo "FAIL: expected experiment_topology=pgbouncer in manifest" >&2
  exit 1
fi

UPGRADE_RUN_ID="test-multi-version-upgrade-smoke-$(date -u +%Y%m%d_%H%M%S)"
EXPERIMENT_RUN_ID="$UPGRADE_RUN_ID" \
  "$REPO_DIR/scripts/run_experiment.sh" run multi-version-upgrade-smoke >/dev/null

UPGRADE_RUN_DIR="$REPO_DIR/runs/$UPGRADE_RUN_ID"
grep -q '"status": "passed"' "$UPGRADE_RUN_DIR/verdict.json"
if [[ "$(manifest_value "$UPGRADE_RUN_DIR/manifest.env" experiment_topology)" != "multi-version-upgrade" ]]; then
  echo "FAIL: expected experiment_topology=multi-version-upgrade in manifest" >&2
  exit 1
fi

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
