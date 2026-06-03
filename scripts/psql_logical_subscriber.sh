#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$REPO_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$REPO_DIR/.env"
  set +a
fi

export POSTGRES_HOST="${POSTGRES_LOGICAL_SUBSCRIBER_HOST:-127.0.0.1}"
export POSTGRES_PORT="${POSTGRES_LOGICAL_SUBSCRIBER_PORT:-55435}"

exec "$REPO_DIR/scripts/psql.sh" "$@"
