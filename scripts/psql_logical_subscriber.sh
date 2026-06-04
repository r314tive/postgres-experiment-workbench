#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

for name in POSTGRES_LOGICAL_SUBSCRIBER_HOST POSTGRES_LOGICAL_SUBSCRIBER_PORT POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD ALLOW_NONLOCAL_PG ALLOW_SYSTEM_DB; do
  if [[ ${!name+x} ]]; then
    PRESERVED_ENV_NAMES+=("$name")
    PRESERVED_ENV_VALUES+=("${!name}")
  fi
done

if [[ -f "$REPO_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$REPO_DIR/.env"
  set +a
fi

for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
  export "${PRESERVED_ENV_NAMES[$i]}=${PRESERVED_ENV_VALUES[$i]}"
done

export POSTGRES_HOST="${POSTGRES_LOGICAL_SUBSCRIBER_HOST:-127.0.0.1}"
export POSTGRES_PORT="${POSTGRES_LOGICAL_SUBSCRIBER_PORT:-55435}"

exec "$REPO_DIR/scripts/psql.sh" "$@"
