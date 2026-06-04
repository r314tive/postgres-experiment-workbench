#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()
ENV_PATH=""
COMPOSE_CMD=()
COMPOSE_ARGS=()

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|COMPOSE|POSTGRES_*|ALLOW_*|TOPOLOGY|TOPOLOGY_*|UPGRADE_*)
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

load_repo_env
compose_command

TOPOLOGY=multi-version-upgrade "$REPO_DIR/scripts/topology.sh" up multi-version-upgrade >/dev/null

"${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T postgres-new sh -lc '
set -eu

dump_file="${UPGRADE_DUMP_FILE:-/tmp/workbench-upgrade.sql}"
rm -f "$dump_file"
export PGPASSWORD="$POSTGRES_PASSWORD"

printf "source_version="
psql \
  -h postgres-old \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  -At \
  -v ON_ERROR_STOP=1 \
  -c "SELECT current_setting('\''server_version'\'')"

printf "target_version="
psql \
  -h 127.0.0.1 \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  -At \
  -v ON_ERROR_STOP=1 \
  -c "SELECT current_setting('\''server_version'\'')"

pg_dump \
  -h postgres-old \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges \
  > "$dump_file"

psql \
  -h 127.0.0.1 \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  -v ON_ERROR_STOP=1 \
  -f "$dump_file"

psql \
  -h 127.0.0.1 \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  -v ON_ERROR_STOP=1 \
  -c "ANALYZE;"

psql \
  -h 127.0.0.1 \
  -p 5432 \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  -x \
  -v ON_ERROR_STOP=1 \
  -c "SELECT current_database() AS database, count(*) AS user_tables FROM pg_class WHERE relkind IN ('\''r'\'','\''p'\'') AND relnamespace NOT IN ('\''pg_catalog'\''::regnamespace, '\''information_schema'\''::regnamespace);"
'
