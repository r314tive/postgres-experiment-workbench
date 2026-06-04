#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

for name in POSTGRES_HOST POSTGRES_PORT POSTGRES_LOGICAL_SUBSCRIBER_HOST POSTGRES_LOGICAL_SUBSCRIBER_PORT POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD ALLOW_NONLOCAL_PG ALLOW_SYSTEM_DB LOGICAL_REPLICATION_TIMEOUT LOGICAL_REPLICATION_INTERVAL LOGICAL_REPLICATION_COMPARE_SQL; do
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

TIMEOUT="${LOGICAL_REPLICATION_TIMEOUT:-60}"
INTERVAL="${LOGICAL_REPLICATION_INTERVAL:-1}"
COMPARE_SQL="${LOGICAL_REPLICATION_COMPARE_SQL:-SELECT count(*) FROM logical_repl.events}"

require_nonnegative_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[0-9]+$ ]]; then
    echo "$label must be a non-negative integer, got: $value" >&2
    exit 2
  fi
}

require_positive_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "$label must be a positive integer, got: $value" >&2
    exit 2
  fi
}

query_primary() {
  "$REPO_DIR/scripts/psql.sh" -At -F '|' -c "$COMPARE_SQL"
}

query_subscriber() {
  "$REPO_DIR/scripts/psql_logical_subscriber.sh" -At -F '|' -c "$COMPARE_SQL"
}

require_nonnegative_int LOGICAL_REPLICATION_TIMEOUT "$TIMEOUT"
require_positive_int LOGICAL_REPLICATION_INTERVAL "$INTERVAL"

deadline=$(( $(date +%s) + TIMEOUT ))
last_primary=""
last_subscriber=""

while true; do
  last_primary="$(query_primary 2>/dev/null || true)"
  last_subscriber="$(query_subscriber 2>/dev/null || true)"

  if [[ -n "$last_primary" && "$last_primary" = "$last_subscriber" ]]; then
    printf 'logical_replication_primary=%s\n' "$last_primary"
    printf 'logical_replication_subscriber=%s\n' "$last_subscriber"
    exit 0
  fi

  if (( $(date +%s) >= deadline )); then
    echo "Logical replication did not converge before timeout" >&2
    printf 'primary=%s\n' "$last_primary" >&2
    printf 'subscriber=%s\n' "$last_subscriber" >&2
    exit 1
  fi

  sleep "$INTERVAL"
done
