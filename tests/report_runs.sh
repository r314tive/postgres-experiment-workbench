#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="$REPO_DIR/.tmp/report/run-a"

rm -rf "$REPO_DIR/.tmp/report"
mkdir -p "$RUN_DIR"

cat > "$RUN_DIR/manifest.env" <<'ENV'
run_id=run-a
started_at=2026-01-01T00:00:00Z
experiment_spec_id=smoke
experiment_topology=single
experiment_pg_config=default
profile=smoke
dataset_spec=synthetic/items
workload_spec=sql/smoke-run
background_specs=profile/locks-blocker
ENV

cat > "$RUN_DIR/verdict.env" <<'ENV'
status=passed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
ENV

cat > "$RUN_DIR/metrics.csv" <<'CSV'
sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes
t0,db,1,0,0,0,5,0,10,0,1,100,10,0,0,0,0,0,10,0,100
t1,db,2,1,1,1,8,1,15,0,2,150,40,1,0,0,1,20,20,0,250
CSV

OUT="$("$REPO_DIR/scripts/report_run.sh" "$RUN_DIR")"
grep -q '# Experiment Run Report' <<< "$OUT"
grep -q '| Status | `passed` |' <<< "$OUT"
grep -q '| `wal_bytes` | `100` | `250` | `150` |' <<< "$OUT"

"$REPO_DIR/scripts/report_run.sh" "$RUN_DIR" "$RUN_DIR/report.md" >/dev/null
grep -q '# Experiment Run Report' "$RUN_DIR/report.md"

GO_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" go run ./cmd/pgworkbench report run "$RUN_DIR")"
grep -q '# Experiment Run Report' <<< "$GO_OUT"
grep -q '| Status | `passed` |' <<< "$GO_OUT"
grep -q '| `wal_bytes` | `100` | `250` | `150` |' <<< "$GO_OUT"

(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" \
  go run ./cmd/pgworkbench report run "$RUN_DIR" "$RUN_DIR/report-go.md") >/dev/null
grep -q '# Experiment Run Report' "$RUN_DIR/report-go.md"

echo "PASS: run reports"
