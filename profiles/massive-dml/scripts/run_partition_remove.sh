#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"

case "${PROFILE_SIZE:-small}" in
  small)
    default_rows=20000
    default_old_rows=8000
    ;;
  medium)
    default_rows=200000
    default_old_rows=80000
    ;;
  large)
    default_rows=1000000
    default_old_rows=400000
    ;;
  *)
    default_rows=20000
    default_old_rows=8000
    ;;
esac

rows="${MASSIVE_DML_PARTITION_ROWS:-$default_rows}"
old_rows="${MASSIVE_DML_PARTITION_OLD_ROWS:-$default_old_rows}"
payload_bytes="${MASSIVE_DML_PARTITION_PAYLOAD_BYTES:-64}"
artifact_dir="${MASSIVE_DML_ARTIFACT_DIR:-}"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/massive-dml"
  fi
fi

mkdir -p "$artifact_dir"
result_file="$artifact_dir/partition-drop-vs-delete.tsv"
metadata_file="$artifact_dir/partition-drop-vs-delete.env"

"$REPO_DIR/scripts/psql.sh" \
  -v "partition_rows=$rows" \
  -v "partition_old_rows=$old_rows" \
  -v "partition_payload_bytes=$payload_bytes" \
  -f "$REPO_DIR/profiles/massive-dml/sql/91_partition_remove_comparison.sql"

"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -P footer=off -c "
SELECT scenario, variant, rows_affected, total_ms, wal_bytes,
       table_bytes, index_bytes, recorded_at
FROM massive_dml.experiment_results
WHERE scenario = 'partition-remove'
ORDER BY variant;
" > "$result_file"

{
  printf 'scenario=partition-remove\n'
  printf 'profile_size=%s\n' "${PROFILE_SIZE:-small}"
  printf 'rows=%s\n' "$rows"
  printf 'old_rows=%s\n' "$old_rows"
  printf 'payload_bytes=%s\n' "$payload_bytes"
  printf 'result_file=%s\n' "$result_file"
} > "$metadata_file"

echo "partition_comparison_result=$result_file"
