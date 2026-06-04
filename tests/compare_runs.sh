#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="$REPO_DIR/.tmp/compare/base"
CAND="$REPO_DIR/.tmp/compare/candidate"

"$REPO_DIR/scripts/compare_runs.sh" --help >/dev/null

rm -rf "$REPO_DIR/.tmp/compare"
mkdir -p "$BASE" "$CAND"

cat > "$BASE/verdict.env" <<'ENV'
status=passed
message=baseline
ENV

cat > "$CAND/verdict.env" <<'ENV'
status=passed
message=candidate
ENV

cat > "$BASE/metrics.csv" <<'CSV'
sampled_at,database_name,temp_bytes,wal_bytes,tup_inserted,tup_updated,tup_deleted
t0,db,0,100,10,20,30
t1,db,10,160,15,30,35
CSV

cat > "$CAND/metrics.csv" <<'CSV'
sampled_at,database_name,temp_bytes,wal_bytes,tup_inserted,tup_updated,tup_deleted
t0,db,0,100,10,20,30
t1,db,20,220,30,40,45
CSV

OUT="$("$REPO_DIR/scripts/compare_runs.sh" "$BASE" "$CAND")"
grep -q '# Run Comparison' <<< "$OUT"
grep -q 'WAL bytes delta' <<< "$OUT"
grep -q '`60`' <<< "$OUT"
grep -q '`120`' <<< "$OUT"

GO_OUT="$(cd "$REPO_DIR" && GOCACHE="$REPO_DIR/.tmp/go-cache" GOMODCACHE="$REPO_DIR/.tmp/go-mod-cache" go run ./cmd/pgworkbench report compare "$BASE" "$CAND")"
grep -q '# Run Comparison' <<< "$GO_OUT"
grep -q 'WAL bytes delta' <<< "$GO_OUT"
grep -q '`60`' <<< "$GO_OUT"
grep -q '`120`' <<< "$GO_OUT"

echo "PASS: run comparison"
