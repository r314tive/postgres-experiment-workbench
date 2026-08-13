#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
artifact_dir="${VACUUM_BLOAT_ARTIFACT_DIR:-}"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/vacuum-bloat"
  fi
fi

mkdir -p "$artifact_dir"
operation_result_file="$artifact_dir/operation-result.json"

"$REPO_DIR/scripts/psql.sh" -v ON_ERROR_STOP=1 -c "
UPDATE vacuum_bloat.events
SET status = 'review', updated_at = clock_timestamp()
WHERE id % 5 = 0;

DELETE FROM vacuum_bloat.events
WHERE id % 13 = 0;

ANALYZE vacuum_bloat.events;
"

LC_ALL=C "$REPO_DIR/scripts/psql.sh" -q -A -t -P footer=off \
  -f "$REPO_DIR/profiles/vacuum-bloat/sql/20_benchmark_vacuum.sql" \
  > "$operation_result_file"

test -s "$operation_result_file"
echo "operation_result=$operation_result_file"
