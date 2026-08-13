#!/usr/bin/env bash

# This file is sourced by run_experiment.sh. It deliberately exposes one
# fixed-output collector rather than a general SQL or path interface.

capture_effective_pg_settings() {
  local names="${PGWORKBENCH_AB_EFFECTIVE_SETTING_NAMES:-}"
  local protocol_digest="${PGWORKBENCH_AB_PROTOCOL_DIGEST:-}"
  local trial="${PGWORKBENCH_BENCHMARK_TRIAL:-}"
  local output temporary previous name
  local -a requested=()

  [[ -n "$names" ]] || return 0

  if [[ -z "${RUN_DIR:-}" || "$RUN_DIR" != /* || ! -d "$RUN_DIR/artifacts/benchmark" || -L "$RUN_DIR" || -L "$RUN_DIR/artifacts" || -L "$RUN_DIR/artifacts/benchmark" ]]; then
    echo "Effective pg_settings collection requires the owned absolute benchmark run directory" >&2
    return 2
  fi
  if [[ -z "${RUN_ID:-}" || "${PGWORKBENCH_BENCHMARK_RUN_ID:-}" != "$RUN_ID" ]]; then
    echo "Effective pg_settings collection run identity is missing or inconsistent" >&2
    return 2
  fi
  if [[ ! "$protocol_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Effective pg_settings collection protocol digest is invalid" >&2
    return 2
  fi
  if [[ ! "$trial" =~ ^[1-9][0-9]*$ ]]; then
    echo "Effective pg_settings collection trial is invalid" >&2
    return 2
  fi

  IFS=',' read -r -a requested <<< "$names"
  if (( ${#requested[@]} == 0 || ${#requested[@]} > 512 )); then
    echo "Effective pg_settings collection requires 1..512 names" >&2
    return 2
  fi
  previous=""
  for name in "${requested[@]}"; do
    if [[ ! "$name" =~ ^[a-z][a-z0-9_.]*$ || ( -n "$previous" && "$name" < "$previous" ) || "$name" = "$previous" ]]; then
      echo "Effective pg_settings names must be safe, sorted, and unique: $name" >&2
      return 2
    fi
    previous="$name"
  done

  output="$RUN_DIR/artifacts/benchmark/effective-pg-settings.tsv"
  if [[ -e "$output" || -L "$output" ]]; then
    echo "Refusing to overwrite effective pg_settings evidence: $output" >&2
    return 2
  fi
  temporary="$(mktemp "$RUN_DIR/artifacts/benchmark/.effective-pg-settings.XXXXXX")"
  chmod 0600 "$temporary"
  printf 'run_id\tprotocol_digest\ttrial\tcaptured_at\tserver_version_num\tname\tsetting\tunit\tsource\tpending_restart\tcontext\n' > "$temporary"
  if ! "$REPO_DIR/scripts/psql.sh" -A -t -F $'\t' \
      -v run_id="$RUN_ID" \
      -v protocol_digest="$protocol_digest" \
      -v trial="$trial" \
      -v setting_names="$names" >> "$temporary" <<'SQL'
WITH requested AS MATERIALIZED (
  SELECT name, ordinal
  FROM unnest(string_to_array(:'setting_names', ',')) WITH ORDINALITY AS requested(name, ordinal)
), observation AS MATERIALIZED (
  SELECT clock_timestamp() AS captured_at,
         current_setting('server_version_num') AS server_version_num
)
SELECT :'run_id',
       :'protocol_digest',
       :'trial',
       to_char(observation.captured_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
       observation.server_version_num,
       settings.name,
       settings.setting,
       COALESCE(settings.unit, ''),
       settings.source,
       settings.pending_restart,
       settings.context
FROM requested
JOIN pg_catalog.pg_settings AS settings USING (name)
CROSS JOIN observation
ORDER BY requested.ordinal;
SQL
  then
    rm -f -- "$temporary"
    echo "Effective pg_settings query failed" >&2
    return 1
  fi
  local lines
  lines="$(wc -l < "$temporary")"
  lines="${lines//[[:space:]]/}"
  if [[ ! "$lines" =~ ^[0-9]+$ || "$lines" -ne $(( ${#requested[@]} + 1 )) ]]; then
    rm -f -- "$temporary"
    echo "Effective pg_settings query did not return every requested name exactly once" >&2
    return 1
  fi
  if ! mv -- "$temporary" "$output"; then
    rm -f -- "$temporary"
    return 1
  fi
  chmod 0600 "$output"
}
