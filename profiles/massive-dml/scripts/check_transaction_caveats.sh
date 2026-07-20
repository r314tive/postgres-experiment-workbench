#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
artifact_dir="${MASSIVE_DML_ARTIFACT_DIR:-}"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/massive-dml"
  fi
fi

mkdir -p "$artifact_dir"

"$REPO_DIR/scripts/run_profile_sql.sh" massive-dml 20_procedure_update.sql >/dev/null
"$REPO_DIR/scripts/run_profile_sql.sh" massive-dml 60_transaction_caveats.sql >/dev/null

external_transaction_log="$artifact_dir/procedure-inside-transaction.log"
temp_table_log="$artifact_dir/temp-table-on-commit-drop.log"

if "$REPO_DIR/scripts/psql.sh" \
  -c "BEGIN; CALL massive_dml.backfill_transaction_log_timestamps(500, 0); COMMIT;" \
  >"$external_transaction_log" 2>&1; then
  echo "Procedure with internal COMMIT unexpectedly succeeded inside an external transaction" >&2
  exit 1
fi

if "$REPO_DIR/scripts/psql.sh" \
  -c "CALL massive_dml.bad_temp_table_on_commit_drop_demo();" \
  >"$temp_table_log" 2>&1; then
  echo "ON COMMIT DROP caveat unexpectedly succeeded" >&2
  exit 1
fi

grep -Eq 'invalid transaction termination|cannot commit' "$external_transaction_log"
grep -Eq 'relation .*tmp_massive_dml_ids.* does not exist' "$temp_table_log"

printf 'expected_failure=procedure_inside_external_transaction\n' \
  > "$artifact_dir/transaction-caveats.env"
printf 'expected_failure=temp_table_on_commit_drop\n' \
  >> "$artifact_dir/transaction-caveats.env"

echo "PASS: procedure inside external transaction failed as expected"
echo "PASS: ON COMMIT DROP caveat failed as expected"
