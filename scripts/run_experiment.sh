#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_experiment.sh list
  scripts/run_experiment.sh show <experiment-spec>
  scripts/run_experiment.sh run <experiment-spec>
  scripts/run_experiment.sh <experiment-spec>

Experiment specs live under experiments/**/*.env and orchestrate profiles,
workloads, background workloads, hooks, metrics, snapshots, assertions, scans,
and verdicts into runs/<run-id>/.
USAGE
}

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

iso_now() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

sanitize_id() {
  printf '%s' "$1" | tr '/ ' '__' | tr -cd '[:alnum:]_.-'
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|COMPOSE|POSTGRES_*|ALLOW_*|TOPOLOGY|TOPOLOGY_*|PG_CONFIG|PROFILE_*|DATASET_*|METRICS_*|WORKLOAD_*|EXPERIMENT_*)
        PRESERVED_ENV_NAMES+=("$name")
        PRESERVED_ENV_VALUES+=("${!name}")
        ;;
    esac
  done < <(compgen -v)
}

restore_env_overrides() {
  local i

  for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
    export "${PRESERVED_ENV_NAMES[$i]}=${PRESERVED_ENV_VALUES[$i]}"
  done
}

list_specs() {
  find "$REPO_DIR/experiments" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/experiments/"}"
    printf '%s\n' "${spec%.env}"
  done
}

resolve_spec() {
  local input="${1:?experiment spec is required}"
  local candidate

  if [[ -f "$input" ]]; then
    realpath "$input"
    return 0
  fi

  candidate="$REPO_DIR/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/experiments/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/experiments/$input.env"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  mapfile -t matches < <(find "$REPO_DIR/experiments" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    local id="${spec#"$REPO_DIR/experiments/"}"
    id="${id%.env}"
    if [[ "$id" = "$input" || "$(basename "$id")" = "$input" ]]; then
      printf '%s\n' "$spec"
    fi
  done)

  if (( ${#matches[@]} == 1 )); then
    realpath "${matches[0]}"
    return 0
  fi

  if (( ${#matches[@]} > 1 )); then
    echo "Ambiguous experiment spec: $input" >&2
    printf '  %s\n' "${matches[@]#"$REPO_DIR/experiments/"}" >&2
    exit 2
  fi

  echo "Experiment spec not found: $input" >&2
  exit 1
}

load_repo_env() {
  local env_file="${ENV_FILE:-}"

  if [[ -z "$env_file" ]]; then
    if [[ -f "$REPO_DIR/.env" ]]; then
      env_file="$REPO_DIR/.env"
    else
      env_file="$REPO_DIR/.env.example"
    fi
  elif [[ "$env_file" != /* ]]; then
    env_file="$REPO_DIR/$env_file"
  fi

  ENV_PATH="$env_file"
  if [[ -f "$ENV_PATH" ]]; then
    capture_env_overrides
    set -a
    # shellcheck disable=SC1090
    source "$ENV_PATH"
    set +a
    restore_env_overrides
  fi
}

load_spec() {
  EXPERIMENT_SPEC_FILE="$(resolve_spec "$1")"
  EXPERIMENT_SPEC_ID="${EXPERIMENT_SPEC_FILE#"$REPO_DIR/experiments/"}"
  EXPERIMENT_SPEC_ID="${EXPERIMENT_SPEC_ID%.env}"

  set -a
  # shellcheck disable=SC1090
  source "$EXPERIMENT_SPEC_FILE"
  set +a
  restore_env_overrides
}

write_manifest() {
  {
    printf 'run_id=%s\n' "$RUN_ID"
    printf 'started_at=%s\n' "$STARTED_AT"
    printf 'experiment_spec=%s\n' "$EXPERIMENT_SPEC_FILE"
    printf 'experiment_spec_id=%s\n' "$EXPERIMENT_SPEC_ID"
    printf 'experiment_name=%s\n' "${EXPERIMENT_NAME:-$EXPERIMENT_SPEC_ID}"
    printf 'experiment_topology=%s\n' "${EXPERIMENT_TOPOLOGY:-single}"
    printf 'experiment_pg_config=%s\n' "${EXPERIMENT_PG_CONFIG:-${PG_CONFIG:-default}}"
    printf 'profile=%s\n' "${EXPERIMENT_PROFILE:-}"
    printf 'dataset_spec=%s\n' "${EXPERIMENT_DATASET_SPEC:-}"
    printf 'profile_size=%s\n' "${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}"
    printf 'workload_spec=%s\n' "${EXPERIMENT_WORKLOAD_SPEC:-}"
    printf 'background_specs=%s\n' "${EXPERIMENT_BACKGROUND_SPECS:-}"
    printf 'run_dir=%s\n' "$RUN_DIR"
  } > "$RUN_DIR/manifest.env"
}

run_psql_file_list() {
  local files="$1"
  local file
  local status=0

  for file in $files; do
    [[ -z "$file" ]] && continue
    if [[ "$file" != /* ]]; then
      file="$REPO_DIR/$file"
    fi
    "$REPO_DIR/scripts/psql.sh" -f "$file" || status="$?"
  done

  return "$status"
}

run_inline_sql() {
  local sql="$1"
  [[ -z "$sql" ]] && return 0
  "$REPO_DIR/scripts/psql.sh" -c "$sql"
}

run_shell_hook() {
  local command="$1"
  [[ -z "$command" ]] && return 0
  export REPO_DIR RUN_ID RUN_DIR EXPERIMENT_SPEC_FILE EXPERIMENT_SPEC_ID
  bash -lc "$command"
}

run_assertions() {
  local status=0

  run_psql_file_list "${EXPERIMENT_ASSERT_SQL_FILES:-}" || status="$?"
  run_inline_sql "${EXPERIMENT_ASSERT_SQL:-}" || status="$?"
  run_shell_hook "${EXPERIMENT_ASSERT_SHELL:-}" || status="$?"

  return "$status"
}

snapshot() {
  local label="$1"

  if [[ "${EXPERIMENT_SNAPSHOT:-1}" = "1" ]]; then
    "$REPO_DIR/scripts/snapshot_pg.sh" "$RUN_DIR/snapshots/$label"
  fi
}

start_metrics() {
  if [[ "${EXPERIMENT_METRICS:-1}" != "1" ]]; then
    return 0
  fi

  METRICS_INTERVAL="${EXPERIMENT_METRICS_INTERVAL:-${METRICS_INTERVAL:-1}}" \
  METRICS_DURATION="${EXPERIMENT_METRICS_DURATION:-${METRICS_DURATION:-30}}" \
  METRICS_SAMPLES="${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}" \
  METRICS_OUT="$RUN_DIR/metrics.csv" \
    "$REPO_DIR/scripts/sample_metrics.sh" > "$RUN_DIR/metrics.log" 2>&1 &
  METRICS_PID="$!"
}

stop_metrics() {
  if [[ -n "${METRICS_PID:-}" ]] && kill -0 "$METRICS_PID" >/dev/null 2>&1; then
    kill "$METRICS_PID" >/dev/null 2>&1 || true
    wait "$METRICS_PID" >/dev/null 2>&1 || true
  fi
}

start_background_specs() {
  local specs="${EXPERIMENT_BACKGROUND_SPECS:-}"
  local spec safe log
  mkdir -p "$RUN_DIR/background"

  for spec in $specs; do
    safe="$(sanitize_id "$spec")"
    log="$RUN_DIR/background/$safe.log"
    WORKLOAD_RUN_LOG=0 \
    WORKLOAD_LOG_DIR="$RUN_DIR/background" \
    PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
    PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
      "$REPO_DIR/scripts/run_workload.sh" run "$spec" > "$log" 2>&1 &
    BACKGROUND_PIDS+=("$!")
    BACKGROUND_LOGS+=("$log")
  done

  if [[ -n "$specs" && "${EXPERIMENT_BACKGROUND_WARMUP:-0}" != "0" ]]; then
    sleep "$EXPERIMENT_BACKGROUND_WARMUP"
  fi
}

stop_background_specs() {
  local pid
  for pid in "${BACKGROUND_PIDS[@]:-}"; do
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
    fi
  done

  for pid in "${BACKGROUND_PIDS[@]:-}"; do
    wait "$pid" >/dev/null 2>&1 || true
  done
}

write_verdict() {
  local status="$1"
  local message="$2"
  local finished_at
  finished_at="$(iso_now)"

  {
    printf 'status=%s\n' "$status"
    printf 'message=%s\n' "$message"
    printf 'finished_at=%s\n' "$finished_at"
    printf 'workload_exit=%s\n' "${WORKLOAD_EXIT:-0}"
    printf 'assert_exit=%s\n' "${ASSERT_EXIT:-0}"
    printf 'scan_exit=%s\n' "${SCAN_EXIT:-0}"
  } > "$RUN_DIR/verdict.env"

  cat > "$RUN_DIR/verdict.json" <<JSON
{
  "run_id": "$(json_escape "$RUN_ID")",
  "status": "$(json_escape "$status")",
  "message": "$(json_escape "$message")",
  "started_at": "$(json_escape "$STARTED_AT")",
  "finished_at": "$(json_escape "$finished_at")",
  "experiment_spec": "$(json_escape "$EXPERIMENT_SPEC_ID")",
  "run_dir": "$(json_escape "$RUN_DIR")",
  "workload_exit": ${WORKLOAD_EXIT:-0},
  "assert_exit": ${ASSERT_EXIT:-0},
  "scan_exit": ${SCAN_EXIT:-0}
}
JSON
}

cleanup() {
  stop_metrics
  stop_background_specs
}

run_experiment() {
  STARTED_AT="$(iso_now)"
  RUN_ID="${EXPERIMENT_RUN_ID:-$(sanitize_id "${EXPERIMENT_SPEC_ID}")-$(timestamp)}"
  RUN_DIR="$REPO_DIR/runs/$RUN_ID"
  METRICS_PID=""
  BACKGROUND_PIDS=()
  BACKGROUND_LOGS=()
  WORKLOAD_EXIT=0
  ASSERT_EXIT=0
  SCAN_EXIT=0

  mkdir -p "$RUN_DIR" "$RUN_DIR/hooks" "$RUN_DIR/snapshots" "$RUN_DIR/artifacts"
  write_manifest

  exec > >(tee -a "$RUN_DIR/stdout.log") 2>&1
  trap cleanup EXIT

  echo "run_id=$RUN_ID"
  echo "run_dir=$RUN_DIR"
  echo "started_at=$STARTED_AT"

  local topology="${EXPERIMENT_TOPOLOGY:-${TOPOLOGY:-single}}"

  if [[ "${EXPERIMENT_DOCKER_RESET:-0}" = "1" ]]; then
    make -C "$REPO_DIR" docker-reset TOPOLOGY="$topology" PG_CONFIG="${EXPERIMENT_PG_CONFIG:-${PG_CONFIG:-default}}"
  else
    make -C "$REPO_DIR" docker-up TOPOLOGY="$topology"
    if [[ -n "${EXPERIMENT_PG_CONFIG:-}" && "${EXPERIMENT_PG_CONFIG:-default}" != "default" ]]; then
      "$REPO_DIR/scripts/apply_pg_config.sh" "$EXPERIMENT_PG_CONFIG"
    fi
  fi

  if [[ -n "${EXPERIMENT_DATASET_SPEC:-}" ]]; then
    DATASET_SIZE="${EXPERIMENT_DATASET_SIZE:-${DATASET_SIZE:-small}}" \
      "$REPO_DIR/scripts/load_dataset.sh" load "$EXPERIMENT_DATASET_SPEC"
  fi

  if [[ -n "${EXPERIMENT_PROFILE:-}" ]]; then
    if [[ "${EXPERIMENT_PROFILE_SETUP:-1}" = "1" ]]; then
      PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
      PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
        "$REPO_DIR/scripts/run_profile_sql.sh" "$EXPERIMENT_PROFILE" 00_setup.sql
    fi

    if [[ "${EXPERIMENT_PROFILE_RUN:-0}" = "1" ]]; then
      PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
      PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
        "$REPO_DIR/scripts/run_profile_sql.sh" "$EXPERIMENT_PROFILE" "${EXPERIMENT_PROFILE_RUN_SQL:-10_run.sql}"
    fi
  fi

  run_psql_file_list "${EXPERIMENT_BEFORE_SQL_FILES:-}"
  run_inline_sql "${EXPERIMENT_BEFORE_SQL:-}"
  run_shell_hook "${EXPERIMENT_BEFORE_SHELL:-}"

  snapshot before
  start_metrics
  start_background_specs

  if [[ -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]]; then
    set +e
    WORKLOAD_LOG_FILE="$RUN_DIR/workload.log" \
    WORKLOAD_LOG_DIR="$RUN_DIR" \
    PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
    PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
      "$REPO_DIR/scripts/run_workload.sh" run "$EXPERIMENT_WORKLOAD_SPEC"
    WORKLOAD_EXIT="$?"
    set -e
  fi

  if [[ "${EXPERIMENT_BACKGROUND_WAIT:-0}" = "1" ]]; then
    for pid in "${BACKGROUND_PIDS[@]:-}"; do
      wait "$pid" || true
    done
  fi

  stop_background_specs
  stop_metrics

  run_psql_file_list "${EXPERIMENT_AFTER_SQL_FILES:-}"
  run_inline_sql "${EXPERIMENT_AFTER_SQL:-}"
  run_shell_hook "${EXPERIMENT_AFTER_SHELL:-}"

  snapshot after

  set +e
  run_assertions
  ASSERT_EXIT="$?"
  set -e

  set +e
  "$REPO_DIR/scripts/scan_pg_failures.sh" "$RUN_DIR" ${EXPERIMENT_SCAN_PATHS:-} > "$RUN_DIR/scan.log" 2>&1
  SCAN_EXIT="$?"
  set -e

  if [[ "$WORKLOAD_EXIT" != "0" ]]; then
    write_verdict failed "workload failed"
    exit "$WORKLOAD_EXIT"
  fi

  if [[ "$ASSERT_EXIT" != "0" ]]; then
    write_verdict failed "assertion failed"
    exit "$ASSERT_EXIT"
  fi

  if [[ "$SCAN_EXIT" != "0" ]]; then
    write_verdict failed "failure evidence found"
    exit "$SCAN_EXIT"
  fi

  write_verdict passed "experiment passed"
  echo "verdict=passed"
}

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$ACTION" in
  help|-h|--help)
    usage
    ;;
  list)
    list_specs
    ;;
  show)
    sed -n '1,220p' "$(resolve_spec "${1:?experiment spec is required}")"
    ;;
  run)
    load_repo_env
    load_spec "${1:?experiment spec is required}"
    run_experiment
    ;;
  *)
    load_repo_env
    load_spec "$ACTION"
    run_experiment
    ;;
esac
