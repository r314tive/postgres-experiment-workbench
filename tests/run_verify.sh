#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_DIR="$REPO_DIR/.tmp/verify"
RUN_DIR="$BASE_DIR/run-a"
BROKEN_DIR="$BASE_DIR/run-b"

rm -rf "$BASE_DIR"
mkdir -p "$RUN_DIR" "$BROKEN_DIR"

write_run() {
  local run_dir="$1"
  local run_id="$2"

  cat > "$run_dir/manifest.env" <<ENV
run_id=$run_id
started_at=2026-01-01T00:00:00Z
experiment_spec=experiments/smoke.env
experiment_spec_id=smoke
experiment_name=smoke experiment
experiment_topology=single
experiment_pg_config=default
profile=smoke
dataset_spec=
profile_size=small
workload_spec=sql/smoke-run
background_specs=
run_dir=$run_dir
ENV

  cat > "$run_dir/verdict.env" <<'ENV'
status=passed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
ENV

  cat > "$run_dir/verdict.json" <<JSON
{
  "run_id": "$run_id",
  "status": "passed",
  "message": "experiment passed",
  "started_at": "2026-01-01T00:00:00Z",
  "finished_at": "2026-01-01T00:00:02Z",
  "experiment_spec": "smoke",
  "run_dir": "$run_dir",
  "workload_exit": 0,
  "assert_exit": 0,
  "scan_exit": 0
}
JSON
}

write_run "$RUN_DIR" run-a
cat > "$RUN_DIR/metrics.csv" <<'CSV'
sampled_at,database_name,wal_bytes
t0,db,100
CSV

OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify "$RUN_DIR")"
grep -q 'PASS: run artifact' <<< "$OUT"

write_run "$BROKEN_DIR" run-b
if BROKEN_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify "$BROKEN_DIR" 2>&1)"; then
  echo "FAIL: expected broken run verification to fail" >&2
  exit 1
fi
grep -q 'missing metrics.csv' <<< "$BROKEN_OUT"

echo "PASS: run verification"
