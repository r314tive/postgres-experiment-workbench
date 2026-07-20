#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
MODE="${1:?Usage: run_generated.sh update|delete}"

case "$MODE" in
  update)
    generator="$REPO_DIR/profiles/massive-dml/sql/11_generate_update_batches.sql"
    artifact_name="generated-update.sql"
    stats_query="SELECT * FROM massive_dml.transaction_log_backfill_stats;"
    ;;
  delete)
    generator="$REPO_DIR/profiles/massive-dml/sql/12_generate_delete_batches.sql"
    artifact_name="generated-delete.sql"
    stats_query="SELECT * FROM massive_dml.audit_record_delete_stats;"
    ;;
  *)
    echo "Unsupported massive-dml generated mode: $MODE" >&2
    exit 2
    ;;
esac

case "${PROFILE_SIZE:-small}" in
  small) default_batch_size=500 ;;
  medium) default_batch_size=5000 ;;
  large) default_batch_size=10000 ;;
  *) default_batch_size=500 ;;
esac

batch_size="${MASSIVE_DML_BATCH_SIZE:-$default_batch_size}"
sleep_seconds="${MASSIVE_DML_SLEEP_SECONDS:-0}"
cutoff="${MASSIVE_DML_CUTOFF:-2026-04-07 00:00:00+00}"
execute="${MASSIVE_DML_EXECUTE:-1}"
artifact_dir="${MASSIVE_DML_ARTIFACT_DIR:-}"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/massive-dml"
  fi
fi

mkdir -p "$artifact_dir"
generated_sql="$artifact_dir/$artifact_name"
metadata_file="$artifact_dir/${artifact_name%.sql}.env"
stats_file="$artifact_dir/${artifact_name%.sql}-after.tsv"

psql_args=(
  -q
  -A
  -t
  -v "batch_size=$batch_size"
  -v "sleep_seconds=$sleep_seconds"
)

if [[ "$MODE" = "delete" ]]; then
  psql_args+=(-v "cutoff='$cutoff'")
fi

"$REPO_DIR/scripts/psql.sh" "${psql_args[@]}" -f "$generator" > "$generated_sql"

if [[ ! -s "$generated_sql" ]]; then
  echo "Generated SQL is empty: $generated_sql" >&2
  exit 1
fi

begin_count="$(grep -c '^BEGIN;$' "$generated_sql" || true)"
commit_count="$(grep -c '^COMMIT;$' "$generated_sql" || true)"

if [[ "$begin_count" -eq 0 || "$begin_count" -ne "$commit_count" ]]; then
  echo "Generated SQL has invalid transaction boundaries: BEGIN=$begin_count COMMIT=$commit_count" >&2
  exit 1
fi

{
  printf 'mode=%s\n' "$MODE"
  printf 'profile_size=%s\n' "${PROFILE_SIZE:-small}"
  printf 'batch_size=%s\n' "$batch_size"
  printf 'sleep_seconds=%s\n' "$sleep_seconds"
  printf 'cutoff=%s\n' "$cutoff"
  printf 'batch_count=%s\n' "$begin_count"
  printf 'execute=%s\n' "$execute"
  printf 'generated_sql=%s\n' "$generated_sql"
} > "$metadata_file"

echo "generated_sql=$generated_sql"
echo "batch_count=$begin_count"

if [[ "$execute" = "0" ]]; then
  echo "execution=skipped"
  exit 0
fi

"$REPO_DIR/scripts/psql.sh" -f "$generated_sql"
"$REPO_DIR/scripts/psql.sh" -A -F $'\t' -P footer=off -c "$stats_query" > "$stats_file"

echo "stats=$stats_file"
