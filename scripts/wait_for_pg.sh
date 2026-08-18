#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=exact_environment.sh
source "$REPO_DIR/scripts/exact_environment.sh"
pgworkbench_initialize_exact_environment

PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

for name in POSTGRES_HOST POSTGRES_PORT POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD POSTGRES_REPLICA_HOST POSTGRES_LOGICAL_SUBSCRIBER_HOST POSTGRES_UPGRADE_OLD_HOST POSTGRES_UPGRADE_NEW_HOST PGBOUNCER_HOST ALLOW_NONLOCAL_PG ALLOW_SYSTEM_DB PGWORKBENCH_EXPERIMENT_MODE PGWORKBENCH_RUNTIME PGWORKBENCH_NATIVE_BINDIR PG_INSTALL_DIR; do
  if [[ ${!name+x} ]]; then
    PRESERVED_ENV_NAMES+=("$name")
    PRESERVED_ENV_VALUES+=("${!name}")
  fi
done

if [[ -f "$REPO_DIR/.env" ]] && ! pgworkbench_exact_environment_active; then
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

if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" = "1" ]]; then
  unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS \
    PGTARGETSESSIONATTRS PGSSLMODE PGSSLROOTCERT PGSSLCERT PGSSLKEY \
    PGREQUIRESSL PGREQUIREAUTH PGCHANNELBINDING
fi

PG_ISREADY_BIN="pg_isready"
if [[ "${PGWORKBENCH_RUNTIME:-docker}" = "native" ]]; then
  native_bindir="${PGWORKBENCH_NATIVE_BINDIR:-}"
  if [[ -z "$native_bindir" && -n "${PG_INSTALL_DIR:-}" ]]; then
    native_bindir="$PG_INSTALL_DIR/bin"
  fi
  if [[ -n "$native_bindir" ]]; then
    [[ "$native_bindir" = /* ]] || native_bindir="$REPO_DIR/$native_bindir"
    PG_ISREADY_BIN="$native_bindir/pg_isready"
    [[ -x "$PG_ISREADY_BIN" ]] || { echo "Required PostgreSQL binary is not executable: $PG_ISREADY_BIN" >&2; exit 2; }
  fi
fi

for _ in {1..60}; do
  if "$PG_ISREADY_BIN" \
    -h "${POSTGRES_HOST:-127.0.0.1}" \
    -p "${POSTGRES_PORT:-55433}" \
    -U "${POSTGRES_USER:-postgres}" \
    -d "${POSTGRES_DB:-pg_experiment_workbench}" >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
done

echo "PostgreSQL is not ready" >&2
exit 1
