#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
mode="${MASSIVE_DML_BULK_LOAD_MODE:-indexed}"

case "$mode" in
  indexed|index-after) ;;
  *)
    echo "Unsupported MASSIVE_DML_BULK_LOAD_MODE: $mode" >&2
    exit 2
    ;;
esac

case "${PROFILE_SIZE:-small}" in
  small) default_rows=20000 ;;
  medium) default_rows=200000 ;;
  large) default_rows=1000000 ;;
  *) default_rows=20000 ;;
esac

rows="${MASSIVE_DML_BULK_ROWS:-$default_rows}"
payload_bytes="${MASSIVE_DML_BULK_PAYLOAD_BYTES:-64}"
artifact_dir="${MASSIVE_DML_ARTIFACT_DIR:-}"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/massive-dml"
  fi
fi

mkdir -p "$artifact_dir"
result_file="$artifact_dir/bulk-load-$mode.tsv"
metadata_file="$artifact_dir/bulk-load-$mode.env"

"$REPO_DIR/scripts/psql.sh" \
  -v "bulk_mode=$mode" \
  -v "bulk_rows=$rows" \
  -v "bulk_payload_bytes=$payload_bytes" \
  -f "$REPO_DIR/profiles/massive-dml/sql/90_bulk_load.sql"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -P footer=off -c "
SELECT scenario, variant, rows_affected, load_ms, index_ms, total_ms,
       wal_bytes, table_bytes, index_bytes, recorded_at
FROM massive_dml.experiment_results
WHERE scenario = 'offline-bulk-load' AND variant = '$mode';
" > "$result_file"

{
  printf 'scenario=offline-bulk-load\n'
  printf 'variant=%s\n' "$mode"
  printf 'profile_size=%s\n' "${PROFILE_SIZE:-small}"
  printf 'rows=%s\n' "$rows"
  printf 'payload_bytes=%s\n' "$payload_bytes"
  printf 'result_file=%s\n' "$result_file"
} > "$metadata_file"

echo "bulk_load_mode=$mode"
echo "bulk_load_result=$result_file"
