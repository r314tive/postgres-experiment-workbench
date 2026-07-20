#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_CACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}"

run_massive_dml_experiment() {
  local spec="$1"
  local safe_spec="${spec//\//-}"
  local run_id="test-${safe_spec}-$(date -u +%Y%m%d_%H%M%S)-$$"
  local run_dir="$REPO_DIR/runs/$run_id"

  EXPERIMENT_RUN_ID="$run_id" \
  EXPERIMENT_PROFILE_SIZE=small \
  EXPERIMENT_SNAPSHOT=0 \
  EXPERIMENT_METRICS_SAMPLES=1 \
    "$REPO_DIR/scripts/run_experiment.sh" run "$spec" >/dev/null

  grep -q '"status": "passed"' "$run_dir/verdict.json"
  "$REPO_DIR/scripts/run_experiment.sh" show "$spec" >/dev/null
  (
    cd "$REPO_DIR"
    GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" \
      go run ./cmd/pgworkbench run verify "$run_dir" >/dev/null
  )

  printf '%s\n' "$run_dir"
}

UPDATE_RUN_DIR="$(run_massive_dml_experiment massive-dml/generated-batched-update)"
test -s "$UPDATE_RUN_DIR/artifacts/generated-update.sql"
test -s "$UPDATE_RUN_DIR/artifacts/generated-update.env"
test -s "$UPDATE_RUN_DIR/artifacts/generated-update-after.tsv"
grep -q '^BEGIN;$' "$UPDATE_RUN_DIR/artifacts/generated-update.sql"
grep -q '^COMMIT;$' "$UPDATE_RUN_DIR/artifacts/generated-update.sql"

DELETE_RUN_DIR="$(run_massive_dml_experiment massive-dml/generated-batched-delete)"
test -s "$DELETE_RUN_DIR/artifacts/generated-delete.sql"
test -s "$DELETE_RUN_DIR/artifacts/generated-delete.env"
test -s "$DELETE_RUN_DIR/artifacts/generated-delete-after.tsv"
grep -q '^BEGIN;$' "$DELETE_RUN_DIR/artifacts/generated-delete.sql"
grep -q '^COMMIT;$' "$DELETE_RUN_DIR/artifacts/generated-delete.sql"

run_massive_dml_experiment massive-dml/procedure-update >/dev/null

run_massive_dml_experiment massive-dml/queue-update >/dev/null
run_massive_dml_experiment massive-dml/procedure-delete >/dev/null

CAVEAT_RUN_DIR="$(run_massive_dml_experiment massive-dml/transaction-caveats)"
test -s "$CAVEAT_RUN_DIR/artifacts/procedure-inside-transaction.log"
test -s "$CAVEAT_RUN_DIR/artifacts/temp-table-on-commit-drop.log"
test -s "$CAVEAT_RUN_DIR/artifacts/transaction-caveats.env"

BULK_INDEXED_RUN_DIR="$(run_massive_dml_experiment massive-dml/offline-bulk-load-indexed)"
test -s "$BULK_INDEXED_RUN_DIR/artifacts/bulk-load-indexed.tsv"
test -s "$BULK_INDEXED_RUN_DIR/artifacts/bulk-load-indexed.env"
grep -q $'offline-bulk-load\tindexed\t' "$BULK_INDEXED_RUN_DIR/artifacts/bulk-load-indexed.tsv"

BULK_AFTER_RUN_DIR="$(run_massive_dml_experiment massive-dml/offline-bulk-load-index-after)"
test -s "$BULK_AFTER_RUN_DIR/artifacts/bulk-load-index-after.tsv"
test -s "$BULK_AFTER_RUN_DIR/artifacts/bulk-load-index-after.env"
grep -q $'offline-bulk-load\tindex-after\t' "$BULK_AFTER_RUN_DIR/artifacts/bulk-load-index-after.tsv"

PARTITION_RUN_DIR="$(run_massive_dml_experiment massive-dml/partition-drop-vs-delete)"
test -s "$PARTITION_RUN_DIR/artifacts/partition-drop-vs-delete.tsv"
test -s "$PARTITION_RUN_DIR/artifacts/partition-drop-vs-delete.env"
grep -q $'partition-remove\tpartition-drop\t' "$PARTITION_RUN_DIR/artifacts/partition-drop-vs-delete.tsv"
grep -q $'partition-remove\trow-delete\t' "$PARTITION_RUN_DIR/artifacts/partition-drop-vs-delete.tsv"

echo "PASS: massive-dml parity and physical-strategy experiments"
