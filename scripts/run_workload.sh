#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck source=exact_environment.sh
source "$REPO_DIR/scripts/exact_environment.sh"
pgworkbench_initialize_exact_environment
# shellcheck source=target_arg_guard.sh
source "$REPO_DIR/scripts/target_arg_guard.sh"
# shellcheck source=benchmark_phase.sh
source "$REPO_DIR/scripts/benchmark_phase.sh"
# shellcheck source=benchmark_control.sh
source "$REPO_DIR/scripts/benchmark_control.sh"
# shellcheck source=benchmark_capsule.sh
source "$REPO_DIR/scripts/benchmark_capsule.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_workload.sh list
  scripts/run_workload.sh show <workload-spec>
  scripts/run_workload.sh prepare <workload-spec>
  scripts/run_workload.sh run <workload-spec> [adapter args...]
  scripts/run_workload.sh collect <workload-spec>
  scripts/run_workload.sh cleanup <workload-spec>
  scripts/run_workload.sh <workload-spec> [adapter args...]

Workload specs live under workloads/**/*.env and are trusted local shell env
files. Supported WORKLOAD_KIND values:
  profile-sql  Run profiles/<PROFILE>/sql/<WORKLOAD_SQL>
  sql          Run SQL=<path> through psql
  pgbench      Run pgbench in the container or from native PostgreSQL binaries
  pg-dump      Dump a database or schema to UTILITY_OUTPUT_FILE
  pg-dumpall   Dump the local workbench cluster to UTILITY_OUTPUT_FILE
  pg-restore   Round-trip a schema through pg_dump/pg_restore
  pg-source-check
               Clone/build/test a PostgreSQL source tree
  noisia       Run scripts/run_noisia.sh
  shell        Run WORKLOAD_CMD on the host
  compose-run  Run WORKLOAD_COMMAND inside WORKLOAD_IMAGE via docker compose

Common environment:
  PGWORKBENCH_RUNTIME=docker|native
  PGWORKBENCH_NATIVE_BINDIR=/path/to/postgresql/bin
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
    PGWORKBENCH_RUNTIME
    PGWORKBENCH_NATIVE_BINDIR
    PGWORKBENCH_NATIVE_WAIT_SECONDS
    PGWORKBENCH_EXPERIMENT_MODE
    PGWORKBENCH_BENCHMARK_PHASE_FILE
    PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE
    PGWORKBENCH_BENCHMARK_PREPARED
    PGWORKBENCH_BENCHMARK_RUN_ID
    PGWORKBENCH_BENCHMARK_TRIAL
    PGWORKBENCH_BENCHMARK_CONTROL_RUN_DIR
    PGWORKBENCH_BENCHMARK_CONTRACT_VERSION
    PGWORKBENCH_BENCHMARK_PROTOCOL_DIGEST
    PGWORKBENCH_BENCHMARK_CACHE_REGIME
    PGWORKBENCH_BENCHMARK_CACHE_TARGET_RELATIONS
    PGWORKBENCH_BENCHMARK_CACHE_MIN_RESIDENT_PCT
    PGWORKBENCH_BENCHMARK_STATISTICS_RESET_POLICY
    PGWORKBENCH_BENCHMARK_STATISTICS_RESET_BOUNDARY
    PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_MODE
    PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES
    PGWORKBENCH_BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT
    PGWORKBENCH_BENCHMARK_RESOURCE_BUDGET_MODE
    PGWORKBENCH_BENCHMARK_CPU_BUDGET_MILLICORES
    PGWORKBENCH_BENCHMARK_MEMORY_BUDGET_MIB
    PGWORKBENCH_BENCHMARK_RESOURCE_SCOPE
    PGWORKBENCH_BENCHMARK_RESOURCE_PROVIDER
    PGWORKBENCH_BENCHMARK_CAPSULE_ROOT
    PGWORKBENCH_BENCHMARK_SERIES_ID
    PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID
    PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST
    PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF
    PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST
    TOPOLOGY
    POSTGRES_HOST
    POSTGRES_PORT
    POSTGRES_DB
    POSTGRES_USER
    POSTGRES_PASSWORD
    POSTGRES_REPLICA_HOST
    POSTGRES_REPLICA_PORT
    POSTGRES_REPLICA_SLOT
    POSTGRES_LOGICAL_SUBSCRIBER_HOST
    POSTGRES_LOGICAL_SUBSCRIBER_PORT
    POSTGRES_UPGRADE_OLD_HOST
    POSTGRES_UPGRADE_OLD_PORT
    POSTGRES_UPGRADE_OLD_IMAGE
    POSTGRES_UPGRADE_NEW_HOST
    POSTGRES_UPGRADE_NEW_PORT
    POSTGRES_UPGRADE_NEW_IMAGE
    ALLOW_NONLOCAL_PG
    ALLOW_SYSTEM_DB
    PGBOUNCER_HOST
    PGBOUNCER_PORT
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
    WORKLOAD_PGHOST
    WORKLOAD_PGPORT
    SQL
    PGBENCH_RESET
    PGBENCH_INIT
    PGBENCH_SCALE
    PGBENCH_CLIENTS
    PGBENCH_THREADS
    PGBENCH_TIME
    PGBENCH_TRANSACTIONS
    PGBENCH_WARMUP_TIME
    PGBENCH_SCRIPT
    PGBENCH_MODE
    PGBENCH_PROTOCOL
    PGBENCH_CONNECT_PER_TRANSACTION
    PGBENCH_RATE
    PGBENCH_LATENCY_LIMIT
    PGBENCH_RANDOM_SEED
    PGBENCH_WARMUP_RANDOM_SEED
    PGBENCH_MEASURE_RANDOM_SEED
    PGBENCH_MAX_TRIES
    PGBENCH_PROGRESS
    PGBENCH_LOG_TRANSACTIONS
    PGBENCH_LOG_SAMPLE_RATE
    PGBENCH_RESULT_FILE
    PGBENCH_RAW_LOG_DIR
    PGBENCH_EXTRA_ARGS
    PGBENCH_TARGET
    UTILITY_OUTPUT_FILE
    UTILITY_ARCHIVE_FILE
    UTILITY_SOURCE_SCHEMA
    UTILITY_TARGET_SCHEMA
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
    NOISIA_CONNINFO
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
  local inherited_experiment_mode="$1"
  local i
  local name

  for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
    name="${PRESERVED_ENV_NAMES[$i]}"
    if [[ "$inherited_experiment_mode" = "1" ]]; then
      case "$name" in
        POSTGRES_*|PGBOUNCER_*|ALLOW_*|WORKLOAD_PGHOST|WORKLOAD_PGPORT|NOISIA_CONNINFO|PGWORKBENCH_EXPERIMENT_MODE|PGWORKBENCH_BENCHMARK_*)
          export "$name=${PRESERVED_ENV_VALUES[$i]}"
          continue
          ;;
      esac
    fi
    case "$name" in
	      PROFILE_SIZE|PROFILE_SECONDS|SQL|POSTGRES_UPGRADE_*|WORKLOAD_IMAGE|WORKLOAD_COMMAND|WORKLOAD_CMD|WORKLOAD_REQUIRES_POSTGRES|WORKLOAD_RUN_LOG|WORKLOAD_LOG_DIR|WORKLOAD_LOG_FILE|PGBENCH_*|PGWORKBENCH_BENCHMARK_*|PG_*|PGBOUNCER_*|UTILITY_*|NOISIA_DURATION|NOISIA_JOBS|NOISIA_EXTRA_ARGS|NOISIA_WAIT_LOCKTIME_MIN|NOISIA_WAIT_LOCKTIME_MAX|NOISIA_TEMP_FILES_RATE|NOISIA_TEMP_FILES_SCALE_FACTOR|LOGICAL_REPLICATION_*)
        export "$name=${PRESERVED_ENV_VALUES[$i]}"
      ;;
    esac
  done

  # A sourced workload spec cannot opt into or out of the experiment-owned
  # safety contract. The caller, not the nested spec, owns this capability.
  export PGWORKBENCH_EXPERIMENT_MODE="$inherited_experiment_mode"
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
  # These caller-owned values must also survive the subsequently sourced
  # workload spec when exact mode intentionally skips the repo env file.
  capture_env_overrides
  if [[ -f "$ENV_PATH" ]] && ! pgworkbench_exact_environment_active; then
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
  "$REPO_DIR/scripts/runtime.sh" up "${TOPOLOGY:-single}"
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

  if benchmark_capsule_active; then
    local expected_id="${PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID:-}"
    if [[ "$input" != "$expected_id" ]]; then
      echo "Benchmark execution requested workload $input, want exact capsule workload ${expected_id:-<empty>}" >&2
      return 2
    fi
    benchmark_capsule_resolve \
      "workloads/$expected_id.env" \
      "${PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST:-}"
    return
  fi

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
  local inherited_experiment_mode="${PGWORKBENCH_EXPERIMENT_MODE:-0}"
  SPEC_FILE="$(resolve_spec "$1")"
  if benchmark_capsule_active; then
    SPEC_ID="${PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID:-}"
  else
    SPEC_ID="${SPEC_FILE#"$REPO_DIR/workloads/"}"
    SPEC_ID="${SPEC_ID%.env}"
  fi

  set -a
  # shellcheck disable=SC1090
  source "$SPEC_FILE"
  set +a
  restore_spec_overrides "$inherited_experiment_mode"

  if benchmark_capsule_active; then
    # Workload specs are executable shell. Detect a self-mutation between the
    # resolver's pre-source check and any adapter action.
    benchmark_capsule_resolve \
      "workloads/${PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID:-}.env" \
      "${PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST:-}" >/dev/null
  fi

  WORKLOAD_KIND="${WORKLOAD_KIND:-}"
  if [[ -z "$WORKLOAD_KIND" ]]; then
    echo "WORKLOAD_KIND is required in $SPEC_FILE" >&2
    exit 2
  fi

  if [[ "$PGWORKBENCH_EXPERIMENT_MODE" = "1" ]]; then
    "$REPO_DIR/scripts/guard_local_pg.sh"
    pgworkbench_reject_experiment_target_args
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

runtime_name() {
  printf '%s\n' "${PGWORKBENCH_RUNTIME:-docker}"
}

require_runtime() {
  case "$(runtime_name)" in
    docker|native)
      ;;
    *)
      echo "Unsupported PGWORKBENCH_RUNTIME: $(runtime_name) (expected docker or native)" >&2
      exit 2
      ;;
  esac
}

scrub_experiment_libpq_environment() {
  if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" = "1" ]]; then
    unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS \
      PGTARGETSESSIONATTRS PGSSLMODE PGSSLROOTCERT PGSSLCERT PGSSLKEY \
      PGREQUIRESSL PGREQUIREAUTH PGCHANNELBINDING
  fi
}

native_binary() {
  local name="$1"
  local bindir="${PGWORKBENCH_NATIVE_BINDIR:-}"
  local binary

  if [[ -z "$bindir" && -n "${PG_INSTALL_DIR:-}" ]]; then
    bindir="$PG_INSTALL_DIR/bin"
  fi
  if [[ -n "$bindir" && "$bindir" != /* ]]; then
    bindir="$REPO_DIR/$bindir"
  fi
  if [[ -n "$bindir" ]]; then
    binary="$bindir/$name"
    if [[ ! -x "$binary" ]]; then
      echo "Required native PostgreSQL binary is not executable: $binary" >&2
      exit 2
    fi
    printf '%s\n' "$binary"
    return 0
  fi

  binary="$(command -v "$name" || true)"
  if [[ -z "$binary" ]]; then
    echo "Required native PostgreSQL binary not found: $name (set PGWORKBENCH_NATIVE_BINDIR)" >&2
    exit 2
  fi
  printf '%s\n' "$binary"
}

validate_pg_identifier() {
  local value="$1"
  local label="$2"

  if ! [[ "$value" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "$label must be a simple PostgreSQL identifier, got: $value" >&2
    exit 2
  fi
}

prepare_utility_output() {
  local relative="$1"
  local label="$2"
  local current="$REPO_DIR"
  local component
  local index
  local output
  local -a components=()

  if [[ -z "$relative" ]]; then
    echo "$label is required" >&2
    exit 2
  fi
  if [[ "$relative" = /* || "$relative" = */ || "$relative" == *\\* ]]; then
    echo "$label must be a portable repository-relative file path: $relative" >&2
    exit 2
  fi

  IFS='/' read -r -a components <<< "$relative"
  if (( ${#components[@]} == 0 )); then
    echo "$label must be a portable repository-relative file path: $relative" >&2
    exit 2
  fi
  for component in "${components[@]}"; do
    if [[ -z "$component" || "$component" = "." || "$component" = ".." || ! "$component" =~ ^[A-Za-z0-9._-]+$ ]]; then
      echo "$label must be a portable repository-relative file path: $relative" >&2
      exit 2
    fi
  done

  # Utility adapters may only write into disposable artifact roots. A reviewed
  # utility spec must never be able to replace source, Git metadata, or other
  # repository state through an output path.
  case "$relative" in
    logs/utility/*|.tmp/utility-output/*)
      ;;
    *)
      echo "$label must be under logs/utility/ or .tmp/utility-output/: $relative" >&2
      exit 2
      ;;
  esac

  for ((index = 0; index < ${#components[@]} - 1; index++)); do
    component="${components[$index]}"
    current="$current/$component"
    if [[ -L "$current" ]]; then
      echo "$label parent must not be a symlink: $current" >&2
      exit 2
    fi
    if [[ -e "$current" && ! -d "$current" ]]; then
      echo "$label parent is not a directory: $current" >&2
      exit 2
    fi
    if [[ ! -e "$current" ]]; then
      mkdir "$current"
    fi
  done

  output="$REPO_DIR/$relative"
  if [[ -L "$output" ]]; then
    echo "$label must not be a symlink: $output" >&2
    exit 2
  fi
  if [[ -e "$output" && ! -f "$output" ]]; then
    echo "$label is not a regular file: $output" >&2
    exit 2
  fi
  printf '%s\n' "$output"
}

run_pg_client() {
  local tool="$1"
  local database="$2"
  shift 2

  local -a args=(
    -h "${POSTGRES_HOST:-127.0.0.1}"
    -p "${POSTGRES_PORT:-55433}"
    -U "${POSTGRES_USER:-postgres}"
  )
  if [[ -n "$database" ]]; then
    args+=(-d "$database")
  fi
  args+=("$@")

  scrub_experiment_libpq_environment

  if [[ "$(runtime_name)" = "native" ]]; then
    local binary
    binary="$(native_binary "$tool")"
    PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$binary" "${args[@]}"
    return
  fi

  # Docker utility clients execute inside the workbench-owned PostgreSQL
  # service. They never use an arbitrary host-side Docker container.
  args[1]=127.0.0.1
  args[3]=5432
  container_exec "$tool" "${args[@]}"
}

write_pg_client_output() {
  local tool="$1"
  local database="$2"
  local output="$3"
  shift 3

  local temporary
  local status=0
  if [[ -L "$output" ]]; then
    echo "utility output must not be a symlink: $output" >&2
    return 2
  fi
  temporary="$(mktemp "${output}.tmp.XXXXXX")"
  run_pg_client "$tool" "$database" "$@" > "$temporary" || status=$?
  if (( status == 0 )) && [[ ! -s "$temporary" ]]; then
    echo "$tool produced an empty output file: $output" >&2
    status=1
  fi
  if (( status != 0 )); then
    rm -f -- "$temporary"
    return "$status"
  fi
  if [[ -L "$output" ]]; then
    echo "utility output became a symlink while writing: $output" >&2
    status=2
  elif [[ -e "$output" && ! -f "$output" ]]; then
    echo "utility output is not a regular file: $output" >&2
    status=2
  else
    mv -f -- "$temporary" "$output" || status=$?
  fi
  if (( status != 0 )); then
    rm -f -- "$temporary"
    return "$status"
  fi
}

run_pg_dump() {
  local output
  local schema="${UTILITY_SOURCE_SCHEMA:-}"
  local -a args=()

  output="$(prepare_utility_output "${UTILITY_OUTPUT_FILE:-}" UTILITY_OUTPUT_FILE)"
  if [[ -n "$schema" ]]; then
    validate_pg_identifier "$schema" UTILITY_SOURCE_SCHEMA
    args+=(--schema "$schema")
  fi
  write_pg_client_output pg_dump "${POSTGRES_DB:-pg_experiment_workbench}" "$output" "${args[@]}"
}

run_pg_dumpall() {
  local output
  local -a args=(--no-role-passwords)

  output="$(prepare_utility_output "${UTILITY_OUTPUT_FILE:-}" UTILITY_OUTPUT_FILE)"
  write_pg_client_output pg_dumpall "dbname=${POSTGRES_DB:-pg_experiment_workbench}" "$output" "${args[@]}"
}

run_pg_restore() {
  local output
  local archive
  local source_schema="${UTILITY_SOURCE_SCHEMA:-}"
  local target_schema="${UTILITY_TARGET_SCHEMA:-}"
  local database="${POSTGRES_DB:-pg_experiment_workbench}"
  local temporary_database="pgw_restore_${$}_${RANDOM}"
  local status=0
  local cleanup_status=0

  validate_pg_identifier "$source_schema" UTILITY_SOURCE_SCHEMA
  validate_pg_identifier "$target_schema" UTILITY_TARGET_SCHEMA
  if [[ "$source_schema" = "$target_schema" ]]; then
    echo "UTILITY_SOURCE_SCHEMA and UTILITY_TARGET_SCHEMA must differ" >&2
    exit 2
  fi

  output="$(prepare_utility_output "${UTILITY_OUTPUT_FILE:-}" UTILITY_OUTPUT_FILE)"
  archive="$(prepare_utility_output "${UTILITY_ARCHIVE_FILE:-}" UTILITY_ARCHIVE_FILE)"
  if [[ "$output" = "$archive" ]]; then
    echo "UTILITY_OUTPUT_FILE and UTILITY_ARCHIVE_FILE must differ" >&2
    exit 2
  fi

  write_pg_client_output pg_dump "$database" "$archive" \
    --format=custom --schema "$source_schema" --no-owner --no-privileges
  run_pg_client psql "$database" -v ON_ERROR_STOP=1 -X \
    -c "CREATE DATABASE \"$temporary_database\""

  run_pg_client pg_restore "$temporary_database" \
    --no-owner --no-privileges < "$archive" || status=$?
  if (( status == 0 )); then
    run_pg_client psql "$temporary_database" -v ON_ERROR_STOP=1 -X \
      -c "ALTER SCHEMA \"$source_schema\" RENAME TO \"$target_schema\"" || status=$?
  fi
  if (( status == 0 )); then
    write_pg_client_output pg_dump "$temporary_database" "$output" \
      --schema "$target_schema" --no-owner --no-privileges || status=$?
  fi
  if (( status == 0 )); then
    run_pg_client psql "$database" -v ON_ERROR_STOP=1 -X \
      -c "DROP SCHEMA IF EXISTS \"$target_schema\" CASCADE" || status=$?
  fi
  if (( status == 0 )); then
    run_pg_client psql "$database" -v ON_ERROR_STOP=1 -X --single-transaction \
      < "$output" || status=$?
  fi

  run_pg_client psql "$database" -v ON_ERROR_STOP=1 -X \
    -c "DROP DATABASE \"$temporary_database\"" || cleanup_status=$?
  if (( status != 0 )); then
    return "$status"
  fi
  if (( cleanup_status != 0 )); then
    return "$cleanup_status"
  fi
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

resolve_pgbench_artifact_file() {
  local output="${1:-}"
  local create="${2:-0}"
  local run_dir

  if [[ -z "$output" ]]; then
    return 0
  fi
  if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" != "1" || -z "${WORKLOAD_LOG_FILE:-}" ]]; then
    echo "PGBENCH_RESULT_FILE is only available inside an experiment-owned run" >&2
    return 2
  fi
  if [[ "$output" != /* ]]; then
    echo "PGBENCH_RESULT_FILE must be an absolute path inside the experiment run: $output" >&2
    return 2
  fi
  run_dir="$(cd "$(dirname "$WORKLOAD_LOG_FILE")" && pwd -P)"
  case "$output" in
    "$run_dir"/*)
      ;;
    *)
      echo "PGBENCH_RESULT_FILE must stay inside the experiment run: $output" >&2
      return 2
      ;;
  esac
  if [[ -L "$output" || ( -e "$output" && ! -f "$output" ) ]]; then
    echo "PGBENCH_RESULT_FILE must be a regular non-symlink file: $output" >&2
    return 2
  fi
  case "$create" in
    0)
      if [[ ! -d "$(dirname "$output")" ]]; then
        echo "PGBENCH_RESULT_FILE parent was not prepared: $output" >&2
        return 2
      fi
      ;;
    1)
      mkdir -p "$(dirname "$output")"
      ;;
    *)
      echo "Invalid pgbench artifact create mode: $create" >&2
      return 2
      ;;
  esac
  printf '%s\n' "$output"
}

resolve_pgbench_artifact_dir() {
  local output="${1:-}"
  local create="${2:-0}"
  local run_dir parent

  if [[ -z "$output" ]]; then
    return 0
  fi
  if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" != "1" || -z "${WORKLOAD_LOG_FILE:-}" ]]; then
    echo "PGBENCH_RAW_LOG_DIR is only available inside an experiment-owned run" >&2
    return 2
  fi
  if [[ "$output" != /* ]]; then
    echo "PGBENCH_RAW_LOG_DIR must be an absolute path inside the experiment run: $output" >&2
    return 2
  fi
  run_dir="$(cd "$(dirname "$WORKLOAD_LOG_FILE")" && pwd -P)"
  case "$output" in
    "$run_dir"/*)
      ;;
    *)
      echo "PGBENCH_RAW_LOG_DIR must stay inside the experiment run: $output" >&2
      return 2
      ;;
  esac
  parent="$(dirname "$output")"
  if [[ -L "$parent" || -L "$output" || ( -e "$output" && ! -d "$output" ) ]]; then
    echo "PGBENCH_RAW_LOG_DIR must be a non-symlink directory: $output" >&2
    return 2
  fi
  case "$create" in
    0)
      if [[ ! -d "$output" ]]; then
        echo "PGBENCH_RAW_LOG_DIR was not prepared: $output" >&2
        return 2
      fi
      ;;
    1)
      mkdir -p "$output"
      ;;
    *)
      echo "Invalid pgbench artifact create mode: $create" >&2
      return 2
      ;;
  esac
  printf '%s\n' "$output"
}

resolve_pgbench_script() {
  local script="${PGBENCH_SCRIPT:-}"

  if [[ -z "$script" ]]; then
    if benchmark_capsule_active && [[ -n "${PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF:-}${PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST:-}" ]]; then
      echo "Benchmark capsule declares a script for a builtin workload" >&2
      return 2
    fi
    return 0
  fi
  if benchmark_capsule_active; then
    if [[ "$script" != "${PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF:-}" ]]; then
      echo "Benchmark workload script declaration differs from its capsule capability" >&2
      return 2
    fi
    benchmark_capsule_resolve "$script" "${PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST:-}"
    return
  fi
  if [[ "$script" != /* ]]; then
    script="$REPO_DIR/$script"
  fi
  if [[ ! -f "$script" ]]; then
    echo "PGBENCH_SCRIPT is not a regular file: $script" >&2
    return 2
  fi
  printf '%s\n' "$script"
}

pgbench_container_resource_id() {
  local resource_id

  if [[ -n "${WORKLOAD_LOG_FILE:-}" ]]; then
    resource_id="$(basename "$(dirname "$WORKLOAD_LOG_FILE")")"
  else
    resource_id="$(sanitize_id "$SPEC_ID")-$$"
  fi
  resource_id="$(sanitize_id "$resource_id")"
  if [[ -z "$resource_id" || "$resource_id" = "." || "$resource_id" = ".." ]]; then
    echo "Cannot derive a safe pgbench container resource id" >&2
    return 2
  fi
  # Keep each /tmp path component below common filesystem limits. Experiment
  # run ids are already unique and immutable inside one workbench runtime.
  printf '%.200s\n' "$resource_id"
}

pgbench_container_script_path() {
  printf '/tmp/pgworkbench-%s-script.sql\n' "$(pgbench_container_resource_id)"
}

pgbench_container_log_dir() {
  printf '/tmp/pgworkbench-%s-raw\n' "$(pgbench_container_resource_id)"
}

cleanup_pgbench() {
  local container_script=""
  local container_log_dir=""

  if [[ "$(runtime_name)" = "native" ]]; then
    return 0
  fi
  if [[ -n "${PGBENCH_SCRIPT:-}" ]]; then
    container_script="$(pgbench_container_script_path)"
  fi
  if [[ "${PGBENCH_LOG_TRANSACTIONS:-0}" = "1" ]]; then
    container_log_dir="$(pgbench_container_log_dir)"
  fi
  if [[ -z "$container_script" && -z "$container_log_dir" ]]; then
    return 0
  fi

  container_exec sh -ceu '
    script_path="$1"
    raw_dir="$2"
    case "$script_path" in
      ""|/tmp/pgworkbench-*-script.sql) ;;
      *) echo "refusing unsafe pgbench script cleanup: $script_path" >&2; exit 2 ;;
    esac
    case "$raw_dir" in
      ""|/tmp/pgworkbench-*-raw) ;;
      *) echo "refusing unsafe pgbench raw-log cleanup: $raw_dir" >&2; exit 2 ;;
    esac
    if [ -n "$script_path" ]; then
      rm -f -- "$script_path"
    fi
    if [ -n "$raw_dir" ]; then
      rm -rf -- "$raw_dir"
    fi
  ' sh "$container_script" "$container_log_dir"
}

prepare_pgbench_runtime_artifacts() {
  local script=""
  local container_script=""
  local container_log_dir=""

  script="$(resolve_pgbench_script)"
  resolve_pgbench_artifact_file "${PGBENCH_RESULT_FILE:-}" 1 >/dev/null
  resolve_pgbench_artifact_dir "${PGBENCH_RAW_LOG_DIR:-}" 1 >/dev/null

  if [[ "$(runtime_name)" = "native" ]]; then
    return 0
  fi

  # Remove any residue left while retrying preparation before publishing fresh
  # phase-2 runtime resources. Published experiment run ids are immutable, so
  # this is normally only relevant after a partially failed prepare action.
  cleanup_pgbench
  if [[ -n "$script" ]]; then
    container_script="$(pgbench_container_script_path)"
    "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" cp "$script" "postgres:$container_script" >/dev/null
  fi
  if [[ "${PGBENCH_LOG_TRANSACTIONS:-0}" = "1" ]]; then
    container_log_dir="$(pgbench_container_log_dir)"
    container_exec mkdir "$container_log_dir"
  fi
}

collect_pgbench() {
  local container_log_dir raw_log_dir

  if [[ "${PGBENCH_LOG_TRANSACTIONS:-0}" != "1" || "$(runtime_name)" = "native" ]]; then
    return 0
  fi
  raw_log_dir="$(resolve_pgbench_artifact_dir "${PGBENCH_RAW_LOG_DIR:-}" 0)"
  if [[ -z "$raw_log_dir" ]]; then
    echo "PGBENCH_RAW_LOG_DIR is required when PGBENCH_LOG_TRANSACTIONS=1" >&2
    return 2
  fi
  container_log_dir="$(pgbench_container_log_dir)"
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" cp "postgres:$container_log_dir/." "$raw_log_dir/" >/dev/null
}

run_pgbench_client() {
  local output_file="${1:-}"
  shift
  local status=0

  if [[ "$(runtime_name)" = "native" ]]; then
    if [[ -z "${pgbench_bin:-}" ]]; then
      pgbench_bin="$(native_binary pgbench)"
    fi
    if [[ -n "$output_file" ]]; then
      PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$pgbench_bin" "$@" > "$output_file" 2>&1 || status=$?
      cat "$output_file"
      return "$status"
    fi
    PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$pgbench_bin" "$@"
    return
  fi

  if [[ -n "$output_file" ]]; then
    container_exec pgbench "$@" > "$output_file" 2>&1 || status=$?
    cat "$output_file"
    return "$status"
  fi
  container_exec pgbench "$@"
}

scrub_empty_pgbench_random_seed() {
  # pgbench reads this variable even for initialization. The typed benchmark
  # runner deliberately exports an empty value to erase any ambient seed, but
  # pgbench treats an exported empty string as an invalid seed rather than as
  # "no seed". Remove only that empty sentinel before every pgbench phase.
  if [[ -z "${PGBENCH_RANDOM_SEED:-}" ]]; then
    unset PGBENCH_RANDOM_SEED
  fi
}

resolve_pgbench_target() {
  local target="${PGBENCH_TARGET:-direct-postgres}"
  local runtime

  runtime="$(runtime_name)"
  case "$target" in
    direct-postgres)
      if [[ "$runtime" = "native" ]]; then
        PGBENCH_TARGET_HOST="${POSTGRES_HOST:-127.0.0.1}"
        PGBENCH_TARGET_PORT="${POSTGRES_PORT:-55433}"
      else
        PGBENCH_TARGET_HOST=127.0.0.1
        PGBENCH_TARGET_PORT=5432
      fi
      PGBENCH_TARGET_ENDPOINT_CONTRACT=pgworkbench.pgbench-target/direct-postgres/v1
      ;;
    pgbouncer)
      if [[ "$runtime" != "docker" ]]; then
        echo "PGBENCH_TARGET=pgbouncer requires PGWORKBENCH_RUNTIME=docker; native benchmark targets are direct PostgreSQL only" >&2
        return 2
      fi
      if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" = "1" &&
            "${EXPERIMENT_TOPOLOGY:-${TOPOLOGY:-single}}" != "pgbouncer" ]]; then
        echo "PGBENCH_TARGET=pgbouncer requires the experiment-owned pgbouncer topology" >&2
        return 2
      fi
      # pgbench executes in the owned postgres service so scripts and raw logs
      # keep the existing lifecycle. Only its measured endpoint crosses the
      # Compose network to the owned PgBouncer service.
      PGBENCH_TARGET_HOST=pgbouncer
      PGBENCH_TARGET_PORT=5432
      PGBENCH_TARGET_ENDPOINT_CONTRACT=pgworkbench.pgbench-target/pgbouncer/v1
      ;;
    *)
      echo "Unsupported PGBENCH_TARGET: $target (expected direct-postgres or pgbouncer)" >&2
      return 2
      ;;
  esac
  PGBENCH_TARGET_NAME="$target"
}

compose_service_image_id() {
  local service="${1:?compose service is required}"
  local image_id

  image_id="$("${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" images -q "$service")"
  if [[ "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    printf '%s\n' "$image_id"
    return 0
  fi
  if [[ "$image_id" =~ ^[0-9a-f]{64}$ ]]; then
    printf 'sha256:%s\n' "$image_id"
    return 0
  fi
  echo "Cannot resolve one immutable image ID for Compose service $service" >&2
  return 2
}

resolve_pgbench_image_identity() {
  if [[ "$(runtime_name)" = "native" ]]; then
    PGBENCH_DRIVER_IMAGE_ID=not-applicable
    PGBENCH_TARGET_IMAGE_ID=not-applicable
    return 0
  fi

  PGBENCH_DRIVER_IMAGE_ID="$(compose_service_image_id postgres)"
  if [[ "$PGBENCH_TARGET_NAME" = "pgbouncer" ]]; then
    PGBENCH_TARGET_IMAGE_ID="$(compose_service_image_id pgbouncer)"
  else
    PGBENCH_TARGET_IMAGE_ID="$PGBENCH_DRIVER_IMAGE_ID"
  fi
}

prepare_pgbench() {
	local scale="${PGBENCH_SCALE:-1}"
	local init="${PGBENCH_INIT:-1}"
	local reset="${PGBENCH_RESET:-0}"
	local pgbench_bin=""

	scrub_empty_pgbench_random_seed
	scrub_experiment_libpq_environment
	prepare_pgbench_runtime_artifacts
	if [[ "$reset" = "1" ]]; then
		pgbench_reset_tables
	fi
	if [[ "$init" != "1" ]]; then
		return 0
	fi
	if [[ "$(runtime_name)" = "native" ]]; then
		pgbench_bin="$(native_binary pgbench)"
		PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$pgbench_bin" \
			-h "${POSTGRES_HOST:-127.0.0.1}" \
			-p "${POSTGRES_PORT:-55433}" \
			-U "${POSTGRES_USER:-postgres}" \
			-i -s "$scale" "${POSTGRES_DB:-pg_experiment_workbench}"
	else
		container_exec pgbench \
			-h 127.0.0.1 -p 5432 -U "${POSTGRES_USER:-postgres}" \
			-i -s "$scale" "${POSTGRES_DB:-pg_experiment_workbench}"
	fi
}

run_pgbench() {
  local clients="${PGBENCH_CLIENTS:-2}"
  local threads="${PGBENCH_THREADS:-1}"
  local time_seconds="${PGBENCH_TIME:-30}"
  local transactions="${PGBENCH_TRANSACTIONS:-}"
  local warmup_time="${PGBENCH_WARMUP_TIME:-0}"
  local script="${PGBENCH_SCRIPT:-}"
  local mode="${PGBENCH_MODE:-builtin}"
  local container_script=""
  local pgbench_bin=""
  local result_file=""
  local raw_log_dir=""
	local log_prefix=""
	local warmup_seed="${PGBENCH_WARMUP_RANDOM_SEED:-${PGBENCH_RANDOM_SEED:-}}"
	local measure_seed="${PGBENCH_MEASURE_RANDOM_SEED:-${PGBENCH_RANDOM_SEED:-}}"
  local lifecycle_owned=0
  local lifecycle_status=0
  local status=0
  local phase_started phase_finished phase_status control_status=0 control_reason=""

  scrub_empty_pgbench_random_seed

  scrub_experiment_libpq_environment

	resolve_pgbench_target

	if [[ "${PGWORKBENCH_BENCHMARK_PREPARED:-0}" != "1" ]]; then
		prepare_pgbench
		lifecycle_owned=1
	fi
	resolve_pgbench_image_identity

  local common_args=()
  local measure_args=()
  local warmup_args=()
  local target_args=()
  target_args=(
    -h "$PGBENCH_TARGET_HOST"
    -p "$PGBENCH_TARGET_PORT"
    -U "${POSTGRES_USER:-postgres}"
  )
  common_args+=(-c "$clients" -j "$threads")

  if [[ -n "${PGBENCH_PROTOCOL:-}" ]]; then
    common_args+=(--protocol="${PGBENCH_PROTOCOL}")
  fi
  if [[ "${PGBENCH_CONNECT_PER_TRANSACTION:-0}" = "1" ]]; then
    common_args+=(--connect)
  fi
  if [[ -n "${PGBENCH_RATE:-}" ]]; then
    common_args+=(--rate="${PGBENCH_RATE}")
  fi
  if [[ -n "${PGBENCH_LATENCY_LIMIT:-}" ]]; then
    common_args+=(--latency-limit="${PGBENCH_LATENCY_LIMIT}")
  fi
  if [[ -n "${PGBENCH_MAX_TRIES:-}" ]]; then
    common_args+=(--max-tries="${PGBENCH_MAX_TRIES}")
  fi
  if [[ -n "${PGBENCH_PROGRESS:-}" ]]; then
    common_args+=(--progress="${PGBENCH_PROGRESS}")
  fi

  if [[ -n "$transactions" ]]; then
    measure_args+=(-t "$transactions")
  else
    measure_args+=(-T "$time_seconds")
  fi
  if [[ "$warmup_time" != "0" ]]; then
    warmup_args+=(-T "$warmup_time")
  fi

  script="$(resolve_pgbench_script)"
  if [[ -n "$script" ]]; then
    if [[ "$(runtime_name)" = "native" ]]; then
      common_args+=(-f "$script")
    else
      container_script="$(pgbench_container_script_path)"
      common_args+=(-f "$container_script")
    fi
  elif [[ "$mode" != "builtin" ]]; then
    common_args+=(-b "$mode")
  fi

  if [[ -n "${PGBENCH_EXTRA_ARGS:-}" ]]; then
    read -r -a extra_args <<< "$PGBENCH_EXTRA_ARGS"
    common_args+=("${extra_args[@]}")
  fi

  result_file="$(resolve_pgbench_artifact_file "${PGBENCH_RESULT_FILE:-}" 0)"
  raw_log_dir="$(resolve_pgbench_artifact_dir "${PGBENCH_RAW_LOG_DIR:-}" 0)"
  if [[ "${PGBENCH_LOG_TRANSACTIONS:-0}" = "1" ]]; then
    if [[ -z "$raw_log_dir" ]]; then
      echo "PGBENCH_RAW_LOG_DIR is required when PGBENCH_LOG_TRANSACTIONS=1" >&2
      return 2
    fi
    if [[ "$(runtime_name)" = "native" ]]; then
      log_prefix="$raw_log_dir/pgbench"
    else
      log_prefix="$(pgbench_container_log_dir)/pgbench"
    fi
    measure_args+=(-l --log-prefix="$log_prefix" --sampling-rate="${PGBENCH_LOG_SAMPLE_RATE:-1}")
  fi

  # Protocol controls have explicit lifecycle boundaries. They must complete
  # before either pgbench window begins, so their work cannot contaminate the
  # warmup or measured durations.
  phase_started="$(benchmark_phase_now)"
  if benchmark_control_v2_active && [[ "${PGWORKBENCH_BENCHMARK_STATISTICS_RESET_BOUNDARY:-none}" = "before-warmup" ]]; then
    if benchmark_control_run_statistics_reset before-warmup; then
      phase_finished="$(benchmark_phase_now)"
      benchmark_phase_append 4 pre-warmup-control passed "$phase_started" "$phase_finished" ""
    else
      control_status="$?"
      phase_finished="$(benchmark_phase_now)"
      benchmark_phase_append 4 pre-warmup-control failed "$phase_started" "$phase_finished" "statistics reset exited $control_status"
      benchmark_phase_append 5 warmup skipped "$phase_finished" "$phase_finished" "not reached after failed pre-warmup-control phase"
      benchmark_phase_append 6 pre-measure-control skipped "$phase_finished" "$phase_finished" "not reached after failed pre-warmup-control phase"
      benchmark_phase_append 7 measure skipped "$phase_finished" "$phase_finished" "not reached after failed pre-warmup-control phase"
      if [[ "$lifecycle_owned" = "1" ]]; then
        cleanup_pgbench || true
      fi
      return "$control_status"
    fi
  else
    phase_finished="$(benchmark_phase_now)"
    if benchmark_control_v2_active; then
      control_reason="statistics reset boundary is not before-warmup"
    else
      control_reason="protocol controls are not enabled"
    fi
    benchmark_phase_append 4 pre-warmup-control skipped "$phase_started" "$phase_finished" "$control_reason"
  fi

  if (( ${#warmup_args[@]} > 0 )); then
    local warmup_common_args=("${common_args[@]}")
    if [[ -n "$warmup_seed" ]]; then
      warmup_common_args+=(--random-seed="$warmup_seed")
    fi
    printf 'pgworkbench_benchmark_phase=warmup\n'
    phase_started="$(benchmark_phase_now)"
	if run_pgbench_client "" "${warmup_common_args[@]}" "${warmup_args[@]}" "${target_args[@]}" "${POSTGRES_DB:-pg_experiment_workbench}"; then
      phase_status=passed
    else
      status="$?"
      phase_status=failed
    fi
    phase_finished="$(benchmark_phase_now)"
    if (( status != 0 )); then
      benchmark_phase_append 5 warmup "$phase_status" "$phase_started" "$phase_finished" "pgbench exited $status"
    else
      benchmark_phase_append 5 warmup "$phase_status" "$phase_started" "$phase_finished" ""
    fi
    if (( status != 0 )); then
      phase_started="$(benchmark_phase_now)"
      benchmark_phase_append 6 pre-measure-control skipped "$phase_started" "$phase_started" "not reached after failed warmup phase"
      benchmark_phase_append 7 measure skipped "$phase_started" "$phase_started" "not reached after failed warmup phase"
      if [[ "$lifecycle_owned" = "1" ]]; then
        cleanup_pgbench || true
      fi
      return "$status"
    fi
  else
    phase_started="$(benchmark_phase_now)"
    phase_finished="$(benchmark_phase_now)"
    benchmark_phase_append 5 warmup skipped "$phase_started" "$phase_finished" "zero warmup duration"
  fi

  phase_started="$(benchmark_phase_now)"
  if benchmark_control_v2_active; then
    control_status=0
    if benchmark_control_run_statistics_reset before-measure; then
      :
    else
      control_status="$?"
    fi
    if (( control_status == 0 )); then
      if benchmark_control_capture_cache; then
        :
      else
        control_status="$?"
      fi
    fi
    phase_finished="$(benchmark_phase_now)"
    if (( control_status != 0 )); then
      benchmark_phase_append 6 pre-measure-control failed "$phase_started" "$phase_finished" "statistics reset or cache capture exited $control_status"
      benchmark_phase_append 7 measure skipped "$phase_finished" "$phase_finished" "not reached after failed pre-measure-control phase"
      if [[ "$lifecycle_owned" = "1" ]]; then
        cleanup_pgbench || true
      fi
      return "$control_status"
    fi
    benchmark_phase_append 6 pre-measure-control passed "$phase_started" "$phase_finished" ""
  else
    phase_finished="$(benchmark_phase_now)"
    benchmark_phase_append 6 pre-measure-control skipped "$phase_started" "$phase_finished" "protocol controls are not enabled"
  fi

  # Keep DBNAME positional for compatibility: pgbench 15/16 use short -d for
  # debug output, while newer versions accept -d as --dbname.
	local measure_common_args=("${common_args[@]}")
	if [[ -n "$measure_seed" ]]; then
		measure_common_args+=(--random-seed="$measure_seed")
	fi
  printf 'pgworkbench_benchmark_phase=measure\n'
  printf 'pgworkbench_benchmark_target=%s endpoint_contract=%s driver_service=%s endpoint_host=%s endpoint_port=%s driver_image_id=%s target_image_id=%s\n' \
    "$PGBENCH_TARGET_NAME" \
    "$PGBENCH_TARGET_ENDPOINT_CONTRACT" \
    "$([[ "$(runtime_name)" = "docker" ]] && printf postgres || printf native-host)" \
    "$PGBENCH_TARGET_HOST" \
    "$PGBENCH_TARGET_PORT" \
    "$PGBENCH_DRIVER_IMAGE_ID" \
    "$PGBENCH_TARGET_IMAGE_ID"
  phase_started="$(benchmark_phase_now)"
	if run_pgbench_client "$result_file" "${measure_common_args[@]}" "${measure_args[@]}" "${target_args[@]}" "${POSTGRES_DB:-pg_experiment_workbench}"; then
    phase_status=passed
  else
    status="$?"
    phase_status=failed
  fi
  phase_finished="$(benchmark_phase_now)"
  if (( status != 0 )); then
    benchmark_phase_append 7 measure "$phase_status" "$phase_started" "$phase_finished" "pgbench exited $status"
  else
    benchmark_phase_append 7 measure "$phase_status" "$phase_started" "$phase_finished" ""
  fi

  # A standalone invocation owns its whole local adapter lifecycle. Benchmark
  # runs set PGWORKBENCH_BENCHMARK_PREPARED=1 and let experiment phases 10 and 11
  # collect and clean these resources explicitly.
  if [[ "$lifecycle_owned" = "1" ]]; then
    if (( status == 0 )); then
      lifecycle_status=0
      collect_pgbench || lifecycle_status="$?"
      if (( lifecycle_status != 0 )); then
        status="$lifecycle_status"
      fi
    fi
    lifecycle_status=0
    cleanup_pgbench || lifecycle_status="$?"
    if (( status == 0 && lifecycle_status != 0 )); then
      status="$lifecycle_status"
    fi
  fi
  return "$status"
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

  BASH_ENV=/dev/null bash --noprofile --norc -c "$WORKLOAD_CMD"
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
  if [[ "$(runtime_name)" = "native" ]]; then
    case "$WORKLOAD_KIND" in
      noisia|compose-run)
        echo "WORKLOAD_KIND=$WORKLOAD_KIND requires PGWORKBENCH_RUNTIME=docker" >&2
        exit 2
        ;;
    esac
  fi

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
    pg-dump)
      run_pg_dump "$@"
      ;;
    pg-dumpall)
      run_pg_dumpall "$@"
      ;;
    pg-restore)
      run_pg_restore "$@"
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

prepare_loaded_workload() {
	case "$WORKLOAD_KIND" in
		pgbench)
			prepare_pgbench
			;;
		*)
			echo "WORKLOAD_KIND=$WORKLOAD_KIND does not implement a separate prepare phase" >&2
			return 2
			;;
	esac
}

collect_loaded_workload() {
	case "$WORKLOAD_KIND" in
		pgbench)
			collect_pgbench
			;;
		*)
			return 0
			;;
	esac
}

cleanup_loaded_workload() {
	case "$WORKLOAD_KIND" in
		pgbench)
			cleanup_pgbench
			;;
		*)
			return 0
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
  prepare)
	load_repo_env
	compose_command
	load_spec "${1:?workload spec is required}"
	require_runtime
	if workload_requires_postgres; then
		ensure_postgres
	fi
	prepare_loaded_workload
	;;
  collect)
	load_repo_env
	compose_command
	load_spec "${1:?workload spec is required}"
	require_runtime
	collect_loaded_workload
	;;
  cleanup)
	load_repo_env
	compose_command
	load_spec "${1:?workload spec is required}"
	require_runtime
	cleanup_loaded_workload
	;;
  run)
    load_repo_env
    compose_command
    load_spec "${1:?workload spec is required}"
    shift
    require_runtime
    if workload_requires_postgres; then
      ensure_postgres
    fi
    run_with_log "$@"
    ;;
  *)
    load_repo_env
    compose_command
    load_spec "$ACTION"
    require_runtime
    if workload_requires_postgres; then
      ensure_postgres
    fi
    run_with_log "$@"
    ;;
esac
