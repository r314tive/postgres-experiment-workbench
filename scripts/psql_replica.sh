#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$REPO_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$REPO_DIR/.env"
  set +a
fi

export POSTGRES_HOST="${POSTGRES_REPLICA_HOST:-127.0.0.1}"
export POSTGRES_PORT="${POSTGRES_REPLICA_PORT:-55434}"

exec "$REPO_DIR/scripts/psql.sh" "$@"
