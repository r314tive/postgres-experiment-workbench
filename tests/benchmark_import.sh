#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export GOCACHE="${GOCACHE:-$TMP_DIR/gocache}"
export GOMODCACHE="${GOMODCACHE:-$TMP_DIR/gomodcache}"
BIN="$TMP_DIR/pgworkbench"
GONOSUMDB='*' GOPROXY=off go build -mod=mod -o "$BIN" "$REPO_DIR/cmd/pgworkbench"

"$BIN" benchmark import sysbench1 \
  --workload oltp_read_write/postgresql \
  "$REPO_DIR/internal/benchmarkimport/testdata/sysbench-1.0-oltp.txt" \
  "$TMP_DIR/sysbench"
"$BIN" benchmark import hammerdb6 \
  --manifest "$REPO_DIR/internal/benchmarkimport/testdata/hammerdb6-mapping.json" \
  "$REPO_DIR/internal/benchmarkimport/testdata/hammerdb6-report.json" \
  "$TMP_DIR/hammerdb"
"$BIN" benchmark import benchbase \
  --manifest "$REPO_DIR/internal/benchmarkimport/testdata/benchbase-mapping.json" \
  "$REPO_DIR/internal/benchmarkimport/testdata/benchbase-histogram.json" \
  "$TMP_DIR/benchbase"

for artifact in sysbench hammerdb benchbase; do
  "$BIN" benchmark import-verify "$TMP_DIR/$artifact/result.json"
done

mkdir "$TMP_DIR/relocated"
mv "$TMP_DIR/hammerdb" "$TMP_DIR/relocated/import"
"$BIN" benchmark import-verify "$TMP_DIR/relocated/import"

"$BIN" benchmark import-bundle "$TMP_DIR/relocated/import" "$TMP_DIR/import-a.tar.gz"
"$BIN" benchmark import-bundle "$TMP_DIR/relocated/import/result.json" "$TMP_DIR/import-b.tar.gz"
cmp "$TMP_DIR/import-a.tar.gz" "$TMP_DIR/import-b.tar.gz"
mkdir "$TMP_DIR/extracted-a" "$TMP_DIR/extracted-b"
tar -xzf "$TMP_DIR/import-a.tar.gz" -C "$TMP_DIR/extracted-a"
tar -xzf "$TMP_DIR/import-a.tar.gz" -C "$TMP_DIR/extracted-b"
bundled_result_a="$(find "$TMP_DIR/extracted-a" -type f -path '*/imports/*/result.json' -print -quit)"
bundled_result_b="$(find "$TMP_DIR/extracted-b" -type f -path '*/imports/*/result.json' -print -quit)"
test -n "$bundled_result_a"
test -n "$bundled_result_b"
"$BIN" benchmark import-verify --bundle "$bundled_result_a"
"$BIN" benchmark import-verify --bundle "$bundled_result_b"
printf '\nERROR: injected bundle tamper\n' >> "$(dirname "$bundled_result_b")/raw/source"
if "$BIN" benchmark import-verify --bundle "$bundled_result_b" >/dev/null 2>&1; then
  echo "tampered import bundle unexpectedly verified" >&2
  exit 1
fi

printf '\nERROR: injected tamper\n' >> "$TMP_DIR/sysbench/raw/source"
if "$BIN" benchmark import-verify "$TMP_DIR/sysbench" >/dev/null 2>&1; then
  echo "tampered sysbench import unexpectedly verified" >&2
  exit 1
fi

echo "PASS: offline benchmark import and portable bundle CLI contract"
