#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

for name in POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD LOGICAL_REPLICATION_PUBLICATION LOGICAL_REPLICATION_SUBSCRIPTION LOGICAL_REPLICATION_SLOT; do
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

PUBLICATION="${LOGICAL_REPLICATION_PUBLICATION:-workbench_logical_pub}"
SUBSCRIPTION="${LOGICAL_REPLICATION_SUBSCRIPTION:-workbench_logical_sub}"
SLOT="${LOGICAL_REPLICATION_SLOT:-workbench_logical_slot}"
PUBLISHER_CONN="host=postgres port=5432 dbname=${POSTGRES_DB:-pg_experiment_workbench} user=${POSTGRES_USER:-postgres} password=${POSTGRES_PASSWORD:-postgres}"

require_identifier() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "$label must be a SQL identifier, got: $value" >&2
    exit 2
  fi
}

require_identifier LOGICAL_REPLICATION_PUBLICATION "$PUBLICATION"
require_identifier LOGICAL_REPLICATION_SUBSCRIPTION "$SUBSCRIPTION"
require_identifier LOGICAL_REPLICATION_SLOT "$SLOT"

TOPOLOGY=logical-replication "$REPO_DIR/scripts/topology.sh" up logical-replication

"$REPO_DIR/scripts/psql.sh" \
  -v publication_name="$PUBLICATION" \
  -f "$REPO_DIR/sql/topology/logical_publisher_setup.sql"

"$REPO_DIR/scripts/psql_logical_subscriber.sh" \
  -v subscription_name="$SUBSCRIPTION" \
  -v publication_name="$PUBLICATION" \
  -v slot_name="$SLOT" \
  -v publisher_conn="$PUBLISHER_CONN" \
  -f "$REPO_DIR/sql/topology/logical_subscriber_setup.sql"
