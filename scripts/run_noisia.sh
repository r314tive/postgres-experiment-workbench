#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_noisia.sh help
  scripts/run_noisia.sh wait-xacts [extra noisia args...]
  scripts/run_noisia.sh temp-files [extra noisia args...]
  scripts/run_noisia.sh cleanup

Environment:
  NOISIA_DURATION=60
  NOISIA_JOBS=2

This script runs noisia inside the Docker Compose network against the workbench
PostgreSQL container. Use it only for local/disposable environments.
USAGE
}

ENV_FILE="${ENV_FILE:-.env}"
if [[ "$ENV_FILE" = /* ]]; then
  ENV_PATH="$ENV_FILE"
else
  ENV_PATH="$REPO_DIR/$ENV_FILE"
fi

PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()
for name in \
  COMPOSE \
  POSTGRES_DB \
  POSTGRES_USER \
  POSTGRES_PASSWORD \
  NOISIA_CONNINFO \
  NOISIA_DURATION \
  NOISIA_JOBS \
  NOISIA_WAIT_LOCKTIME_MIN \
  NOISIA_WAIT_LOCKTIME_MAX \
  NOISIA_TEMP_FILES_RATE \
  NOISIA_TEMP_FILES_SCALE_FACTOR
do
  if [[ ${!name+x} ]]; then
    PRESERVED_ENV_NAMES+=("$name")
    PRESERVED_ENV_VALUES+=("${!name}")
  fi
done

if [[ -f "$ENV_PATH" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_PATH"
  set +a
fi

for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
  export "${PRESERVED_ENV_NAMES[$i]}=${PRESERVED_ENV_VALUES[$i]}"
done

read -r -a COMPOSE_CMD <<< "${COMPOSE:-docker compose}"
COMPOSE_ARGS=()
if [[ -f "$ENV_PATH" ]]; then
  COMPOSE_ARGS+=(--env-file "$ENV_PATH")
fi

POSTGRES_DB="${POSTGRES_DB:-pg_experiment_workbench}"
POSTGRES_USER="${POSTGRES_USER:-postgres}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-postgres}"
NOISIA_CONNINFO="${NOISIA_CONNINFO:-host=postgres port=5432 dbname=$POSTGRES_DB user=$POSTGRES_USER password=$POSTGRES_PASSWORD sslmode=disable}"
NOISIA_DURATION="${NOISIA_DURATION:-60}"
NOISIA_JOBS="${NOISIA_JOBS:-2}"

require_increasing_range() {
  local label="$1"
  local min_value="$2"
  local max_value="$3"

  if (( min_value >= max_value )); then
    echo "$label requires min < max, got min=$min_value max=$max_value" >&2
    exit 2
  fi
}

WORKLOAD="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$WORKLOAD" in
  help|-h|--help)
    usage
    "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" run --rm noisia --help
    exit 0
    ;;
  wait-xacts)
    require_increasing_range \
      "wait-xacts locktime" \
      "${NOISIA_WAIT_LOCKTIME_MIN:-5}" \
      "${NOISIA_WAIT_LOCKTIME_MAX:-15}"
    WORKLOAD_ARGS=(
      --wait-xacts
      --wait-xacts.locktime-min="${NOISIA_WAIT_LOCKTIME_MIN:-5}"
      --wait-xacts.locktime-max="${NOISIA_WAIT_LOCKTIME_MAX:-15}"
    )
    ;;
  temp-files)
    WORKLOAD_ARGS=(
      --temp-files
      --temp-files.rate="${NOISIA_TEMP_FILES_RATE:-2}"
      --temp-files.scale-factor="${NOISIA_TEMP_FILES_SCALE_FACTOR:-10}"
    )
    ;;
  cleanup)
    WORKLOAD_ARGS=(--cleanup)
    ;;
  *)
    usage >&2
    echo "Unknown noisia workload: $WORKLOAD" >&2
    exit 2
    ;;
esac

"${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" up -d postgres
"$REPO_DIR/scripts/wait_for_pg.sh"

RUN_ARGS=(--conninfo "$NOISIA_CONNINFO")
if [[ "$WORKLOAD" != "cleanup" ]]; then
  RUN_ARGS+=(--duration "$NOISIA_DURATION" --jobs "$NOISIA_JOBS")
fi

"${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" run --rm noisia \
  "${RUN_ARGS[@]}" \
  "${WORKLOAD_ARGS[@]}" \
  "$@"
