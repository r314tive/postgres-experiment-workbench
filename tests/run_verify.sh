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
JSON_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --json "$RUN_DIR")"
grep -q '"valid": true' <<< "$JSON_OUT"
grep -q '"issues": \[\]' <<< "$JSON_OUT"
if MISSING_INVENTORY_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --bundle "$RUN_DIR" 2>&1)"; then
  echo "FAIL: bundle verification accepted a live run without inventory" >&2
  exit 1
fi
grep -q 'bundle verification requires a complete inventory' <<< "$MISSING_INVENTORY_OUT"

printf 'original evidence\n' > "$RUN_DIR/evidence.txt"
BUNDLE="$BASE_DIR/run-a.tar.gz"
(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run bundle "$RUN_DIR" "$BUNDLE" >/dev/null)

for copy in missing-inventory changed deleted; do
  mkdir -p "$BASE_DIR/$copy"
  tar -C "$BASE_DIR/$copy" -xzf "$BUNDLE"
  (cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
    go run ./cmd/pgworkbench run verify --bundle "$BASE_DIR/$copy/run-a" >/dev/null)
done

rm "$BASE_DIR/missing-inventory/run-a/.pgworkbench-bundle.json"
if REMOVED_INVENTORY_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --bundle "$BASE_DIR/missing-inventory/run-a" 2>&1)"; then
  echo "FAIL: bundle verification accepted an extracted bundle after inventory removal" >&2
  exit 1
fi
grep -q 'bundle verification requires a complete inventory' <<< "$REMOVED_INVENTORY_OUT"

printf 'tampered evidence\n' > "$BASE_DIR/changed/run-a/evidence.txt"
if CHANGED_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --bundle "$BASE_DIR/changed/run-a" 2>&1)"; then
  echo "FAIL: bundle verification accepted changed evidence" >&2
  exit 1
fi
grep -q 'digest mismatch for evidence.txt' <<< "$CHANGED_OUT"

rm "$BASE_DIR/deleted/run-a/evidence.txt"
if DELETED_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --bundle "$BASE_DIR/deleted/run-a" 2>&1)"; then
  echo "FAIL: bundle verification accepted deleted evidence" >&2
  exit 1
fi
grep -q 'missing inventoried file: evidence.txt' <<< "$DELETED_OUT"

write_run "$BROKEN_DIR" run-b
if BROKEN_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify "$BROKEN_DIR" 2>&1)"; then
  echo "FAIL: expected broken run verification to fail" >&2
  exit 1
fi
grep -q 'missing metrics.csv' <<< "$BROKEN_OUT"
if BROKEN_JSON="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench run verify --json "$BROKEN_DIR" 2>&1)"; then
  echo "FAIL: expected broken JSON run verification to fail" >&2
  exit 1
fi
grep -q '"valid": false' <<< "$BROKEN_JSON"
grep -q 'missing metrics.csv' <<< "$BROKEN_JSON"

echo "PASS: live-run and complete-bundle verification"
