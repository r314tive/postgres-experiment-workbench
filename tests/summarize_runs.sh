#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="$REPO_DIR/.tmp/summary"
RUN_A="$BASE/run-a"
RUN_B="$BASE/run-b"
SERIES="$BASE/repeat"

rm -rf "$BASE"
mkdir -p "$RUN_A" "$RUN_B" "$SERIES"

cat > "$RUN_A/manifest.env" <<'ENV'
run_id=run-a
experiment_spec_id=smoke
experiment_pg_config=default
profile_size=small
workload_spec=sql/smoke-run
ENV

cat > "$RUN_B/manifest.env" <<'ENV'
run_id=run-b
experiment_spec_id=smoke
experiment_pg_config=default
profile_size=small
workload_spec=sql/smoke-run
ENV

cat > "$RUN_A/verdict.env" <<'ENV'
status=passed
message=ok
workload_exit=0
assert_exit=0
scan_exit=0
ENV

cat > "$RUN_B/verdict.env" <<'ENV'
status=passed
message=ok
workload_exit=0
assert_exit=0
scan_exit=0
ENV

cat > "$RUN_A/metrics.csv" <<'CSV'
sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes
t0,db,1,0,0,0,5,0,10,0,1,100,10,0,0,0,0,0,0,10,0,100
t1,db,3,1,1,1,8,1,15,0,2,150,40,1,0,0,0,1,20,20,0,250
CSV

cat > "$RUN_B/metrics.csv" <<'CSV'
sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes
t0,db,2,0,0,0,7,0,20,0,1,100,20,0,0,0,0,0,0,10,0,100
t1,db,4,0,0,0,9,0,30,0,3,180,80,2,1,0,0,2,30,40,0,350
CSV

cat > "$SERIES/runs.tsv" <<ENV
iteration	run_id	exit_code	status	message	run_dir
1	run-a	0	passed	ok	$RUN_A
2	run-b	0	passed	ok	$RUN_B
ENV

OUT="$("$REPO_DIR/scripts/summarize_runs.sh" "$SERIES")"
grep -q '# Run Series Summary' <<< "$OUT"
grep -q '| `passed` | `2` |' <<< "$OUT"
grep -q '| `wal_bytes` | `2` | `150` | `200.000` | `250` | `50.000` |' <<< "$OUT"
grep -q '| `active_sessions` | `2` | `3` | `3.500` | `4` | `0.500` |' <<< "$OUT"

"$REPO_DIR/scripts/summarize_runs.sh" --output "$SERIES/statistics.md" "$SERIES" >/dev/null
grep -q '# Run Series Summary' "$SERIES/statistics.md"

echo "PASS: run series summaries"
