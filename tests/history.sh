#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="$REPO_DIR/.tmp/history"
SERIES_A="$BASE/series-a"
SERIES_B="$BASE/series-b"

rm -rf "$BASE"
mkdir -p "$SERIES_A" "$SERIES_B"

write_run() {
  local run_dir="$1"
  local run_id="$2"
  local status="$3"
  local wal_start="$4"
  local wal_end="$5"
  local active_start="$6"
  local active_end="$7"

  mkdir -p "$run_dir"

  cat > "$run_dir/manifest.env" <<ENV
run_id=$run_id
experiment_spec_id=smoke
experiment_pg_config=default
profile_size=small
workload_spec=sql/smoke-run
ENV

  cat > "$run_dir/verdict.env" <<ENV
status=$status
message=ok
workload_exit=0
assert_exit=0
scan_exit=0
ENV

  cat > "$run_dir/metrics.csv" <<CSV
sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes
t0,db,$active_start,0,0,0,5,0,0,0,0,0,0,0,0,0,0,0,0,0,0,$wal_start
t1,db,$active_end,0,0,0,7,0,0,0,0,0,0,0,0,0,0,0,0,0,0,$wal_end
CSV
}

write_run "$SERIES_A/run-a1" run-a1 passed 100 200 1 3
write_run "$SERIES_A/run-a2" run-a2 passed 100 300 2 4
write_run "$SERIES_B/run-b1" run-b1 passed 100 400 3 5
write_run "$SERIES_B/run-b2" run-b2 passed 100 600 4 6

cat > "$SERIES_A/runs.tsv" <<ENV
iteration	run_id	exit_code	status	message	run_dir
1	run-a1	0	passed	ok	$SERIES_A/run-a1
2	run-a2	0	passed	ok	$SERIES_A/run-a2
ENV

cat > "$SERIES_B/runs.tsv" <<ENV
iteration	run_id	exit_code	status	message	run_dir
1	run-b1	0	passed	ok	$SERIES_B/run-b1
2	run-b2	0	passed	ok	$SERIES_B/run-b2
ENV

OUT="$("$REPO_DIR/scripts/compare_run_history.sh" "$SERIES_A" "$SERIES_B")"
grep -q '# Run History Comparison' <<< "$OUT"
grep -q '| `history/series-a` | `2` | `2` | `0` | `0` |' <<< "$OUT"
grep -q '| `history/series-b` | `2` | `2` | `0` | `0` |' <<< "$OUT"
grep -q '| `wal_bytes` | `150` | `400` | `250` |' <<< "$OUT"
grep -q '| `active_sessions` | `3.500` | `5.500` | `2` |' <<< "$OUT"

"$REPO_DIR/scripts/compare_run_history.sh" --output "$BASE/history.md" "$SERIES_A" "$SERIES_B" >/dev/null
grep -q '# Run History Comparison' "$BASE/history.md"

echo "PASS: run history comparison"
