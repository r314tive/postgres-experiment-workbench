#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_workload.sh list
  scripts/run_workload.sh show <workload-spec>
  scripts/run_workload.sh run <workload-spec> [adapter args...]
  scripts/run_workload.sh <workload-spec> [adapter args...]

Workload specs live under workloads/**/*.env and are trusted local shell env
files. Supported WORKLOAD_KIND values:
  profile-sql  Run profiles/<PROFILE>/sql/<WORKLOAD_SQL>
  sql          Run SQL=<path> through psql
  pgbench      Run pgbench inside the postgres container
  pg-source-check
               Clone/build/test a PostgreSQL source tree
  noisia       Run scripts/run_noisia.sh
  shell        Run WORKLOAD_CMD on the host
  compose-run  Run WORKLOAD_COMMAND inside WORKLOAD_IMAGE via docker compose

Common environment:
  WORKLOAD_RUN_LOG=1
  WORKLOAD_LOG_DIR=logs/workloads
  WORKLOAD_REQUIRES_POSTGRES=1
  PROFILE_SIZE=small
  PROFILE_SECONDS=30
USAGE
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  local names=(
    ENV_FILE
    COMPOSE
    POSTGRES_HOST
    POSTGRES_PORT
    POSTGRES_DB
    POSTGRES_USER
    POSTGRES_PASSWORD
    POSTGRES_CONTAINER
    POSTGRES_UPGRADE_OLD_HOST
    POSTGRES_UPGRADE_OLD_PORT
    POSTGRES_UPGRADE_OLD_CONTAINER
    POSTGRES_UPGRADE_OLD_IMAGE
    POSTGRES_UPGRADE_NEW_HOST
    POSTGRES_UPGRADE_NEW_PORT
    POSTGRES_UPGRADE_NEW_CONTAINER
    POSTGRES_UPGRADE_NEW_IMAGE
    PGBOUNCER_HOST
    PGBOUNCER_PORT
    PGBOUNCER_CONTAINER
    PGBOUNCER_IMAGE
    PGBOUNCER_POOL_MODE
    PGBOUNCER_AUTH_TYPE
    PGBOUNCER_MAX_CLIENT_CONN
    PGBOUNCER_DEFAULT_POOL_SIZE
    PGBOUNCER_MIN_POOL_SIZE
    PGBOUNCER_RESERVE_POOL_SIZE
    PGBOUNCER_IGNORE_STARTUP_PARAMETERS
    PGBOUNCER_ADMIN_USERS
    PGBOUNCER_STATS_USERS
    PROFILE
    PROFILE_SIZE
    PROFILE_SECONDS
    WORKLOAD
    WORKLOAD_SQL
    WORKLOAD_SPEC
    WORKLOAD_KIND
    WORKLOAD_IMAGE
    WORKLOAD_COMMAND
    WORKLOAD_CMD
    WORKLOAD_REQUIRES_POSTGRES
    WORKLOAD_RUN_LOG
    WORKLOAD_LOG_DIR
    WORKLOAD_LOG_FILE
    SQL
    PGBENCH_RESET
    PGBENCH_INIT
    PGBENCH_SCALE
    PGBENCH_CLIENTS
    PGBENCH_THREADS
    PGBENCH_TIME
    PGBENCH_TRANSACTIONS
    PGBENCH_SCRIPT
    PGBENCH_MODE
    PGBENCH_EXTRA_ARGS
    PG_REPO_URL
    PG_REF
    PG_PATCHSET
    PG_SOURCE_ACTION
    PG_SOURCE_RUN_ID
    PG_SOURCE_RUN_DIR
    PG_SOURCE_DIR
    PG_INSTALL_DIR
    PG_ARTIFACT_DIR
    PG_PATCH_DIR
    PG_CHECK_TARGET
    PG_MAKE_JOBS
    PG_CLONE_DEPTH
    PG_CONFIGURE_ARGS
    PG_BUILD_CFLAGS
    PG_TEST_INITDB_EXTRA_OPTS
    PG_SOURCE_KEEP_GOING
    PG_UPGRADE_IMAGE
    PG_UPGRADE_ACTION
    PG_UPGRADE_OLD_BINDIR
    PG_UPGRADE_NEW_BINDIR
    PG_UPGRADE_OLD_DATADIR
    PG_UPGRADE_NEW_DATADIR
    NOISIA_IMAGE
    NOISIA_PLATFORM
    NOISIA_DURATION
    NOISIA_JOBS
    NOISIA_WORKLOAD
    NOISIA_EXTRA_ARGS
    NOISIA_WAIT_LOCKTIME_MIN
    NOISIA_WAIT_LOCKTIME_MAX
    NOISIA_TEMP_FILES_RATE
    NOISIA_TEMP_FILES_SCALE_FACTOR
    LOGICAL_REPLICATION_PUBLICATION
    LOGICAL_REPLICATION_SUBSCRIPTION
    LOGICAL_REPLICATION_SLOT
    LOGICAL_REPLICATION_TIMEOUT
    LOGICAL_REPLICATION_INTERVAL
    LOGICAL_REPLICATION_COMPARE_SQL
  )

  for name in "${names[@]}"; do
    if [[ ${!name+x} ]]; then
      PRESERVED_ENV_NAMES+=("$name")
      PRESERVED_ENV_VALUES+=("${!name}")
    fi
  done
}

restore_env_overrides() {
  local i

  for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
    export "${PRESERVED_ENV_NAMES[$i]}=${PRESERVED_ENV_VALUES[$i]}"
  done
}

restore_spec_overrides() {
  local i
  local name

  for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
    name="${PRESERVED_ENV_NAMES[$i]}"
    case "$name" in
      PROFILE_SIZE|PROFILE_SECONDS|SQL|POSTGRES_UPGRADE_*|WORKLOAD_IMAGE|WORKLOAD_COMMAND|WORKLOAD_CMD|WORKLOAD_REQUIRES_POSTGRES|WORKLOAD_RUN_LOG|WORKLOAD_LOG_DIR|WORKLOAD_LOG_FILE|PGBENCH_*|PG_*|PGBOUNCER_*|NOISIA_DURATION|NOISIA_JOBS|NOISIA_EXTRA_ARGS|NOISIA_WAIT_LOCKTIME_MIN|NOISIA_WAIT_LOCKTIME_MAX|NOISIA_TEMP_FILES_RATE|NOISIA_TEMP_FILES_SCALE_FACTOR|LOGICAL_REPLICATION_*)
        export "$name=${PRESERVED_ENV_VALUES[$i]}"
        ;;
    esac
  done
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

compose_command() {
  read -r -a COMPOSE_CMD <<< "${COMPOSE:-docker compose}"
  COMPOSE_ARGS=()
  if [[ -n "${ENV_PATH:-}" && -f "$ENV_PATH" ]]; then
    COMPOSE_ARGS+=(--env-file "$ENV_PATH")
  fi
}

ensure_postgres() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" up -d postgres
  "$REPO_DIR/scripts/wait_for_pg.sh"
}

list_specs() {
  if [[ ! -d "$REPO_DIR/workloads" ]]; then
    return 0
  fi

  find "$REPO_DIR/workloads" -type f -name '*.env' | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/workloads/"}"
    printf '%s\n' "${spec%.env}"
  done
}

resolve_spec() {
  local input="${1:?workload spec is required}"
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

  candidate="$REPO_DIR/workloads/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/workloads/$input.env"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  mapfile -t matches < <(find "$REPO_DIR/workloads" -type f -name '*.env' | sort | while read -r spec; do
    local id="${spec#"$REPO_DIR/workloads/"}"
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
    echo "Ambiguous workload spec: $input" >&2
    printf '  %s\n' "${matches[@]#"$REPO_DIR/workloads/"}" >&2
    exit 2
  fi

  echo "Workload spec not found: $input" >&2
  exit 1
}

load_spec() {
  SPEC_FILE="$(resolve_spec "$1")"
  SPEC_ID="${SPEC_FILE#"$REPO_DIR/workloads/"}"
  SPEC_ID="${SPEC_ID%.env}"

  set -a
  # shellcheck disable=SC1090
  source "$SPEC_FILE"
  set +a
  restore_spec_overrides

  WORKLOAD_KIND="${WORKLOAD_KIND:-}"
  if [[ -z "$WORKLOAD_KIND" ]]; then
    echo "WORKLOAD_KIND is required in $SPEC_FILE" >&2
    exit 2
  fi
}

database_url() {
  printf 'postgres://%s:%s@%s:%s/%s?sslmode=disable' \
    "${POSTGRES_USER:-postgres}" \
    "${POSTGRES_PASSWORD:-postgres}" \
    "${POSTGRES_HOST:-127.0.0.1}" \
    "${POSTGRES_PORT:-55433}" \
    "${POSTGRES_DB:-pg_experiment_workbench}"
}

run_profile_sql() {
  local profile="${PROFILE:-}"
  local sql_name="${WORKLOAD_SQL:-10_run.sql}"

  if [[ -z "$profile" ]]; then
    echo "PROFILE is required for WORKLOAD_KIND=profile-sql" >&2
    exit 2
  fi

  PROFILE_SIZE="${PROFILE_SIZE:-small}" \
  PROFILE_SECONDS="${PROFILE_SECONDS:-30}" \
    "$REPO_DIR/scripts/run_profile_sql.sh" "$profile" "$sql_name"
}

run_sql() {
  local sql_file="${SQL:-${WORKLOAD_SQL:-}}"

  if [[ -z "$sql_file" ]]; then
    echo "SQL or WORKLOAD_SQL is required for WORKLOAD_KIND=sql" >&2
    exit 2
  fi

  if [[ "$sql_file" != /* ]]; then
    sql_file="$REPO_DIR/$sql_file"
  fi

  "$REPO_DIR/scripts/psql.sh" \
    -v profile="${PROFILE:-}" \
    -v profile_size="${PROFILE_SIZE:-small}" \
    -v profile_seconds="${PROFILE_SECONDS:-30}" \
    -f "$sql_file"
}

container_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T \
    -e PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" \
    postgres "$@"
}

pgbench_reset_tables() {
  "$REPO_DIR/scripts/psql.sh" -v ON_ERROR_STOP=1 -c "
DROP TABLE IF EXISTS
    public.pgbench_accounts,
    public.pgbench_branches,
    public.pgbench_history,
    public.pgbench_tellers;
"
}

run_pgbench() {
  local scale="${PGBENCH_SCALE:-1}"
  local clients="${PGBENCH_CLIENTS:-2}"
  local threads="${PGBENCH_THREADS:-1}"
  local time_seconds="${PGBENCH_TIME:-30}"
  local transactions="${PGBENCH_TRANSACTIONS:-}"
  local script="${PGBENCH_SCRIPT:-}"
  local init="${PGBENCH_INIT:-1}"
  local reset="${PGBENCH_RESET:-0}"
  local mode="${PGBENCH_MODE:-builtin}"
  local container_script=""

  if [[ "$reset" = "1" ]]; then
    pgbench_reset_tables
  fi

  if [[ "$init" = "1" ]]; then
    container_exec pgbench \
      -h 127.0.0.1 \
      -p 5432 \
      -U "${POSTGRES_USER:-postgres}" \
      -i \
      -s "$scale" \
      "${POSTGRES_DB:-pg_experiment_workbench}"
  fi

  local args=(
    -h 127.0.0.1
    -p 5432
    -U "${POSTGRES_USER:-postgres}"
    -c "$clients"
    -j "$threads"
  )

  if [[ -n "$transactions" ]]; then
    args+=(-t "$transactions")
  else
    args+=(-T "$time_seconds")
  fi

  if [[ -n "$script" ]]; then
    if [[ "$script" != /* ]]; then
      script="$REPO_DIR/$script"
    fi
    container_script="/tmp/workbench-pgbench-${SPEC_ID//\//-}.sql"
    "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" cp "$script" "postgres:$container_script" >/dev/null
    args+=(-f "$container_script")
  elif [[ "$mode" != "builtin" ]]; then
    args+=(-b "$mode")
  fi

  if [[ -n "${PGBENCH_EXTRA_ARGS:-}" ]]; then
    read -r -a extra_args <<< "$PGBENCH_EXTRA_ARGS"
    args+=("${extra_args[@]}")
  fi

  args+=("${POSTGRES_DB:-pg_experiment_workbench}")

  container_exec pgbench "${args[@]}"
}

run_noisia() {
  local workload="${NOISIA_WORKLOAD:-${WORKLOAD:-help}}"

  if [[ -n "${NOISIA_EXTRA_ARGS:-}" ]]; then
    read -r -a noisia_extra_args <<< "$NOISIA_EXTRA_ARGS"
  else
    noisia_extra_args=()
  fi

  "$REPO_DIR/scripts/run_noisia.sh" "$workload" "${noisia_extra_args[@]}" "$@"
}

run_pg_source_check() {
  "$REPO_DIR/scripts/run_pg_source_check.sh" "${PG_SOURCE_ACTION:-run}"
}

run_shell() {
  if [[ -z "${WORKLOAD_CMD:-}" ]]; then
    echo "WORKLOAD_CMD is required for WORKLOAD_KIND=shell" >&2
    exit 2
  fi

  export REPO_DIR
  export ENV_PATH
  export DATABASE_URL
  export PGHOST="${POSTGRES_HOST:-127.0.0.1}"
  export PGPORT="${POSTGRES_PORT:-55433}"
  export PGDATABASE="${POSTGRES_DB:-pg_experiment_workbench}"
  export PGUSER="${POSTGRES_USER:-postgres}"
  export PGPASSWORD="${POSTGRES_PASSWORD:-postgres}"
  DATABASE_URL="$(database_url)"

  bash -lc "$WORKLOAD_CMD"
}

run_compose() {
  export WORKLOAD_IMAGE="${WORKLOAD_IMAGE:-postgres:16-alpine}"
  export WORKLOAD_COMMAND="${WORKLOAD_COMMAND:-${WORKLOAD_CMD:-true}}"
  export POSTGRES_DB="${POSTGRES_DB:-pg_experiment_workbench}"
  export POSTGRES_USER="${POSTGRES_USER:-postgres}"
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-postgres}"

  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" run --rm workload "$@"
}

run_loaded_workload() {
  case "$WORKLOAD_KIND" in
    profile-sql)
      run_profile_sql "$@"
      ;;
    sql)
      run_sql "$@"
      ;;
    pgbench)
      run_pgbench "$@"
      ;;
    pg-source-check)
      run_pg_source_check "$@"
      ;;
    noisia)
      run_noisia "$@"
      ;;
    shell)
      run_shell "$@"
      ;;
    compose-run)
      run_compose "$@"
      ;;
    *)
      echo "Unsupported WORKLOAD_KIND: $WORKLOAD_KIND" >&2
      exit 2
      ;;
  esac
}

workload_requires_postgres() {
  if [[ "${WORKLOAD_REQUIRES_POSTGRES:-1}" = "0" ]]; then
    return 1
  fi

  case "$WORKLOAD_KIND" in
    pg-source-check)
      return 1
      ;;
    *)
      return 0
      ;;
  esac
}

sanitize_id() {
  printf '%s' "$1" | tr '/ ' '__' | tr -cd '[:alnum:]_.-'
}

run_with_log() {
  local log_dir="${WORKLOAD_LOG_DIR:-$REPO_DIR/logs/workloads}"
  local log_file="${WORKLOAD_LOG_FILE:-$log_dir/$(sanitize_id "$SPEC_ID").$(date -u +%Y%m%d_%H%M%S).log}"

  mkdir -p "$log_dir"

  if [[ "${WORKLOAD_RUN_LOG:-1}" = "0" ]]; then
    printf 'started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'workload_spec=%s\n' "$SPEC_FILE"
    printf 'workload_kind=%s\n' "$WORKLOAD_KIND"
    run_loaded_workload "$@"
    printf 'finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    return 0
  fi

  {
    printf 'started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'workload_spec=%s\n' "$SPEC_FILE"
    printf 'workload_kind=%s\n' "$WORKLOAD_KIND"
    printf 'log_file=%s\n' "$log_file"
    run_loaded_workload "$@"
    printf 'finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } 2>&1 | tee "$log_file"
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
    SPEC_FILE="$(resolve_spec "${1:?workload spec is required}")"
    sed -n '1,220p' "$SPEC_FILE"
    ;;
  run)
    load_repo_env
    compose_command
    load_spec "${1:?workload spec is required}"
    shift
    if workload_requires_postgres; then
      ensure_postgres
    fi
    run_with_log "$@"
    ;;
  *)
    load_repo_env
    compose_command
    load_spec "$ACTION"
    if workload_requires_postgres; then
      ensure_postgres
    fi
    run_with_log "$@"
    ;;
esac
