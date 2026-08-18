#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
artifact_dir="${LOGICAL_BENCHMARK_ARTIFACT_DIR:-}"
# These are protocol constants, not ambient tuning knobs. Changing them needs
# a new operation spec so the measurement scope/digest changes with the run.
poll_interval="0.1"
timeout_seconds="60"
marker_id="9000000000000000000"

if [[ -z "$artifact_dir" ]]; then
  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    artifact_dir="$(dirname "$WORKLOAD_LOG_FILE")/artifacts"
  else
    artifact_dir="$REPO_DIR/generated/logical-replication"
  fi
fi

mkdir -p "$artifact_dir"
operation_result_file="$artifact_dir/operation-result.json"
metadata_file="$artifact_dir/logical-convergence.env"

# The bulk mutation is ordered before the marker in the publication stream.
PROFILE="logical-replication" WORKLOAD_SQL="10_run.sql" \
  "$REPO_DIR/scripts/run_profile_sql.sh" logical-replication 10_run.sql

# created_at is sampled on the publisher before the marker transaction commits.
# Observing this marker therefore produces a conservative upper-bound interval
# that includes commit/apply/poll delay rather than pretending to be pure apply
# latency.
marker_started_at="$(
  "$REPO_DIR/scripts/psql.sh" -qAtX -P footer=off \
    -v marker_id="$marker_id" <<'SQL'
INSERT INTO logical_repl.events (id, tenant_id, payload, created_at)
VALUES (:'marker_id'::bigint, 0, 'pgworkbench-logical-marker', clock_timestamp())
RETURNING created_at::text;
SQL
)"
if [[ -z "$marker_started_at" || "$marker_started_at" = *$'\n'* ]]; then
  echo "Publisher did not return one marker timestamp" >&2
  exit 1
fi

deadline=$(( $(date +%s) + timeout_seconds ))
polls=0
subscriber_marker=""
while true; do
  polls=$((polls + 1))
  subscriber_marker="$(
    "$REPO_DIR/scripts/psql_logical_subscriber.sh" -qAtX -P footer=off \
      -c "SELECT created_at::text FROM logical_repl.events WHERE id = $marker_id::bigint" \
      2>/dev/null || true
  )"
  if [[ "$subscriber_marker" = "$marker_started_at" ]]; then
    break
  fi
  if (( $(date +%s) >= deadline )); then
    echo "Logical replication marker was not observed before timeout" >&2
    exit 1
  fi
  sleep "$poll_interval"
done

marker_detected_at="$("$REPO_DIR/scripts/psql.sh" -qAtX -P footer=off -c 'SELECT clock_timestamp()::text')"
if [[ -z "$marker_detected_at" || "$marker_detected_at" = *$'\n'* ]]; then
  echo "Publisher did not return one detection-boundary timestamp" >&2
  exit 1
fi

compare_sql="SELECT count(*), coalesce(sum(id), 0), coalesce(sum(length(payload)), 0), coalesce(sum(CASE WHEN updated_at IS NULL THEN 0 ELSE 1 END), 0) FROM logical_repl.events"
primary_signature="$("$REPO_DIR/scripts/psql.sh" -qAtX -F '|' -c "$compare_sql")"
subscriber_signature="$("$REPO_DIR/scripts/psql_logical_subscriber.sh" -qAtX -F '|' -c "$compare_sql")"
if [[ -z "$primary_signature" || "$primary_signature" != "$subscriber_signature" ]]; then
  echo "Logical replication signatures differ after marker observation" >&2
  printf 'primary=%s\nsubscriber=%s\n' "$primary_signature" "$subscriber_signature" >&2
  exit 1
fi

scope="Publisher server-clock interval from marker row timestamp before commit through a publisher clock sample after subscriber detection; includes commit, apply, client round trips, and polling delay; poll interval is ${poll_interval} seconds."
"$REPO_DIR/scripts/psql.sh" -qAtX -P footer=off \
  -v marker_started_at="$marker_started_at" \
  -v marker_detected_at="$marker_detected_at" \
  -v measurement_scope="$scope" > "$operation_result_file" <<'SQL'
SELECT jsonb_build_object(
  'schema_version', 'pgworkbench.operation-result/v1',
  'artifact_type', 'pgworkbench.operation-result',
  'operation_id', 'replication/logical-marker-convergence',
  'variant', 'single-subscription-marker-upper-bound',
  'primary_metric', jsonb_build_object(
    'name', 'marker_convergence_ms',
    'unit', 'milliseconds',
    'direction', 'lower-is-better',
    'value', round(
      extract(epoch FROM (:'marker_detected_at'::timestamptz - :'marker_started_at'::timestamptz)) * 1000,
      3
    )
  ),
  'measurement', jsonb_build_object(
    'basis', 'postgres-server-clock',
    'scope', :'measurement_scope'
  )
)::text;
SQL

test -s "$operation_result_file"
{
  printf 'marker_id=%s\n' "$marker_id"
  printf 'marker_started_at=%s\n' "$marker_started_at"
  printf 'marker_detected_at=%s\n' "$marker_detected_at"
  printf 'poll_interval_seconds=%s\n' "$poll_interval"
  printf 'polls=%s\n' "$polls"
  printf 'primary_signature=%s\n' "$primary_signature"
  printf 'subscriber_signature=%s\n' "$subscriber_signature"
  printf 'operation_result_file=%s\n' "$operation_result_file"
} > "$metadata_file"

echo "operation_result=$operation_result_file"
