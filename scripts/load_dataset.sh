#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/load_dataset.sh list
  scripts/load_dataset.sh show <dataset-spec>
  scripts/load_dataset.sh load <dataset-spec>
  scripts/load_dataset.sh <dataset-spec>

Dataset specs live under datasets/**/*.env. Supported DATASET_KIND values:
  sql      Run DATASET_SQL with dataset variables.
  profile  Run a profile setup SQL as a dataset source.
  pgbench  Initialize pgbench tables.
USAGE
}

list_specs() {
  find "$REPO_DIR/datasets" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/datasets/"}"
    printf '%s\n' "${spec%.env}"
  done
}

resolve_spec() {
  local input="${1:?dataset spec is required}"
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

  candidate="$REPO_DIR/datasets/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/datasets/$input.env"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  echo "Dataset spec not found: $input" >&2
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
  return 0
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|COMPOSE|POSTGRES_*|PGBOUNCER_*|ALLOW_*|DATASET_*|PROFILE_*|PGBENCH_*|WORKLOAD_*)
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

load_dataset() {
  DATASET_SPEC_FILE="$(resolve_spec "$1")"
  set -a
  # shellcheck disable=SC1090
  source "$DATASET_SPEC_FILE"
  set +a

  case "${DATASET_KIND:-}" in
    sql)
      sql_file="${DATASET_SQL:?DATASET_SQL is required}"
      if [[ "$sql_file" != /* ]]; then
        sql_file="$REPO_DIR/$sql_file"
      fi
      "$REPO_DIR/scripts/psql.sh" \
        -v dataset_schema="${DATASET_SCHEMA:-dataset_synthetic}" \
        -v dataset_size="${DATASET_SIZE:-small}" \
        -v dataset_rows="${DATASET_ROWS:-10000}" \
        -v dataset_seed="${DATASET_SEED:-1}" \
        -f "$sql_file"
      ;;
    profile)
      PROFILE_SIZE="${DATASET_SIZE:-small}" \
        "$REPO_DIR/scripts/run_profile_sql.sh" "${DATASET_PROFILE:?DATASET_PROFILE is required}" 00_setup.sql
      ;;
    pgbench)
      PGBENCH_RESET="${PGBENCH_RESET:-1}" \
      PGBENCH_INIT=1 \
      PGBENCH_SCALE="${DATASET_SCALE:-1}" \
      PGBENCH_TIME=1 \
      PGBENCH_CLIENTS=1 \
      PGBENCH_THREADS=1 \
      WORKLOAD_RUN_LOG=0 \
        "$REPO_DIR/scripts/run_workload.sh" run workloads/pgbench/tiny.env
      ;;
    *)
      echo "Unsupported DATASET_KIND: ${DATASET_KIND:-}" >&2
      exit 2
      ;;
  esac
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
    sed -n '1,220p' "$(resolve_spec "${1:?dataset spec is required}")"
    ;;
  load)
    load_repo_env
    load_dataset "${1:?dataset spec is required}"
    ;;
  *)
    load_repo_env
    load_dataset "$ACTION"
    ;;
esac
