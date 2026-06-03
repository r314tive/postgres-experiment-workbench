#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

for name in POSTGRES_HOST POSTGRES_PORT POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD ALLOW_NONLOCAL_PG ALLOW_SYSTEM_DB; do
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

export PGPASSWORD="${POSTGRES_PASSWORD:-postgres}"

"$REPO_DIR/scripts/guard_local_pg.sh"

exec psql \
  -h "${POSTGRES_HOST:-127.0.0.1}" \
  -p "${POSTGRES_PORT:-55433}" \
  -U "${POSTGRES_USER:-postgres}" \
  -d "${POSTGRES_DB:-pg_experiment_workbench}" \
  -v ON_ERROR_STOP=1 \
  -X \
  "$@"
