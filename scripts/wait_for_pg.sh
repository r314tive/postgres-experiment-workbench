#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$REPO_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$REPO_DIR/.env"
  set +a
fi

export PGPASSWORD="${POSTGRES_PASSWORD:-postgres}"

"$REPO_DIR/scripts/guard_local_pg.sh"

for _ in {1..60}; do
  if pg_isready \
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
