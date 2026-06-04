#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()
ENV_PATH=""
COMPOSE_CMD=()
COMPOSE_ARGS=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_native_pg_upgrade.sh [plan|check|run]

Native pg_upgrade is opt-in. The default plan mode prints the required image,
bindir, and datadir contract. check/run modes require PG_UPGRADE_OLD_BINDIR and
PG_UPGRADE_NEW_BINDIR inside PG_UPGRADE_IMAGE.
USAGE
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|COMPOSE|POSTGRES_*|PG_UPGRADE_*|ALLOW_*|TOPOLOGY|TOPOLOGY_*)
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
  if [[ -n "$ENV_PATH" && -f "$ENV_PATH" ]]; then
    COMPOSE_ARGS+=(--env-file "$ENV_PATH")
  fi
}

quote() {
  printf '%q' "$1"
}

print_plan() {
  cat <<PLAN
PG_UPGRADE_ACTION=${ACTION}
PG_UPGRADE_IMAGE=${PG_UPGRADE_IMAGE:-postgres:16-alpine}
PG_UPGRADE_OLD_BINDIR=${PG_UPGRADE_OLD_BINDIR:-}
PG_UPGRADE_NEW_BINDIR=${PG_UPGRADE_NEW_BINDIR:-}
PG_UPGRADE_OLD_DATADIR=${PG_UPGRADE_OLD_DATADIR:-/var/lib/postgresql/old-data}
PG_UPGRADE_NEW_DATADIR=${PG_UPGRADE_NEW_DATADIR:-/var/lib/postgresql/new-data}
POSTGRES_USER=${POSTGRES_USER:-postgres}

Required for check/run:
- build or provide PG_UPGRADE_IMAGE with both old and new PostgreSQL binaries;
- set PG_UPGRADE_OLD_BINDIR and PG_UPGRADE_NEW_BINDIR inside that image;
- create old/new data volumes with TOPOLOGY=multi-version-upgrade first;
- keep this adapter opt-in; default CI uses plan only.
PLAN
}

require_native_upgrade_config() {
  if [[ -z "${PG_UPGRADE_OLD_BINDIR:-}" ]]; then
    echo "PG_UPGRADE_OLD_BINDIR is required for native pg_upgrade $ACTION" >&2
    exit 2
  fi

  if [[ -z "${PG_UPGRADE_NEW_BINDIR:-}" ]]; then
    echo "PG_UPGRADE_NEW_BINDIR is required for native pg_upgrade $ACTION" >&2
    exit 2
  fi
}

run_native_upgrade() {
  local command="pg_upgrade"

  require_native_upgrade_config

  if [[ "$ACTION" = "check" ]]; then
    command+=" --check"
  fi

  command+=" --old-bindir $(quote "$PG_UPGRADE_OLD_BINDIR")"
  command+=" --new-bindir $(quote "$PG_UPGRADE_NEW_BINDIR")"
  command+=" --old-datadir $(quote "${PG_UPGRADE_OLD_DATADIR:-/var/lib/postgresql/old-data}")"
  command+=" --new-datadir $(quote "${PG_UPGRADE_NEW_DATADIR:-/var/lib/postgresql/new-data}")"
  command+=" --username $(quote "${POSTGRES_USER:-postgres}")"

  if [[ -n "${PG_UPGRADE_EXTRA_ARGS:-}" ]]; then
    command+=" ${PG_UPGRADE_EXTRA_ARGS}"
  fi

  TOPOLOGY=multi-version-upgrade "$REPO_DIR/scripts/topology.sh" down multi-version-upgrade >/dev/null

  PG_UPGRADE_COMMAND="$command" \
    "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile upgrade run --rm pg-upgrade-native
}

REQUESTED_ACTION="${1:-}"
load_repo_env
compose_command
ACTION="${REQUESTED_ACTION:-${PG_UPGRADE_ACTION:-plan}}"

case "$ACTION" in
  help|-h|--help)
    usage
    ;;
  plan)
    print_plan
    ;;
  check|run)
    print_plan
    run_native_upgrade
    ;;
  *)
    echo "Unsupported pg_upgrade action: $ACTION" >&2
    usage >&2
    exit 2
    ;;
esac
