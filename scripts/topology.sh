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
  scripts/topology.sh list
  scripts/topology.sh show <topology>
  scripts/topology.sh up [topology]
  scripts/topology.sh reset [topology]
  scripts/topology.sh down [topology]
  scripts/topology.sh status [topology]
  scripts/topology.sh wait [topology]

Implemented topologies:
  single               One PostgreSQL container.
  primary-replica      Primary plus one physical streaming replica.
  logical-replication  Publisher plus one logical subscriber.
  pgbouncer            PostgreSQL plus PgBouncer pooler.
  multi-version-upgrade
                       Old and new PostgreSQL versions for upgrade testing.
  source-tree          Alias for single-node PostgreSQL runtime.
USAGE
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|COMPOSE|POSTGRES_*|PGBOUNCER_*|ALLOW_*|TOPOLOGY|TOPOLOGY_*|WORKLOAD_PG*|LOGICAL_REPLICATION_*)
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

list_topologies() {
  find "$REPO_DIR/topologies" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/topologies/"}"
    printf '%s\n' "${spec%.env}"
  done
}

resolve_topology_spec() {
  local topology="${1:?topology is required}"
  local candidate="$REPO_DIR/topologies/$topology.env"

  if [[ -f "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi

  echo "Topology spec not found: $topology" >&2
  exit 1
}

require_topology() {
  local topology="$1"
  case "$topology" in
    single|primary-replica|logical-replication|pgbouncer|multi-version-upgrade|source-tree)
      ;;
    *)
      echo "Unsupported topology: $topology" >&2
      exit 2
      ;;
  esac
}

require_slot_name() {
  local slot="$1"
  if ! [[ "$slot" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "POSTGRES_REPLICA_SLOT must be a valid replication slot identifier, got: $slot" >&2
    exit 2
  fi
}

compose_down() {
  local _profile="$1"
  shift || true

  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" \
    --profile replica \
    --profile logical \
    --profile pgbouncer \
    --profile upgrade \
    --profile workload \
    down "$@"
}

up_primary() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" up -d postgres
  "$REPO_DIR/scripts/wait_for_pg.sh"
}

wait_primary() {
  "$REPO_DIR/scripts/wait_for_pg.sh"
}

primary_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T postgres "$@"
}

replica_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T replica "$@"
}

logical_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T logical-subscriber "$@"
}

pgbouncer_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T pgbouncer "$@"
}

upgrade_old_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T postgres-old "$@"
}

upgrade_new_exec() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T postgres-new "$@"
}

configure_primary_for_replica() {
  local slot="${POSTGRES_REPLICA_SLOT:-workbench_replica_slot}"
  require_slot_name "$slot"

  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T -e REPLICA_SLOT="$slot" postgres sh -lc '
set -eu
if ! grep -q "^host replication all all" "$PGDATA/pg_hba.conf"; then
  printf "%s\n" "host replication all all scram-sha-256" >> "$PGDATA/pg_hba.conf"
fi
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = '\''$REPLICA_SLOT'\'') THEN PERFORM pg_create_physical_replication_slot('\''$REPLICA_SLOT'\''); END IF; END \$\$;"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT pg_reload_conf();"
'
}

up_replica() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile replica up -d replica
  wait_replica
}

wait_replica() {
  for _ in {1..90}; do
    if replica_exec pg_isready \
      -h 127.0.0.1 \
      -p 5432 \
      -U "${POSTGRES_USER:-postgres}" \
      -d "${POSTGRES_DB:-pg_experiment_workbench}" >/dev/null 2>&1; then
      if replica_exec psql \
        -h 127.0.0.1 \
        -p 5432 \
        -U "${POSTGRES_USER:-postgres}" \
        -d "${POSTGRES_DB:-pg_experiment_workbench}" \
        -At \
        -v ON_ERROR_STOP=1 \
        -c "SELECT pg_is_in_recovery()" 2>/dev/null | grep -q '^t$'; then
        return 0
      fi
    fi
    sleep 1
  done

  echo "Replica is not ready" >&2
  exit 1
}

up_logical_subscriber() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile logical up -d logical-subscriber
  wait_logical_subscriber
}

wait_logical_subscriber() {
  for _ in {1..90}; do
    if logical_exec pg_isready \
      -h 127.0.0.1 \
      -p 5432 \
      -U "${POSTGRES_USER:-postgres}" \
      -d "${POSTGRES_DB:-pg_experiment_workbench}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "Logical subscriber is not ready" >&2
  exit 1
}

up_pgbouncer() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile pgbouncer up -d pgbouncer
  wait_pgbouncer
}

wait_pgbouncer() {
  for _ in {1..90}; do
    if pgbouncer_exec env PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" psql \
      -h 127.0.0.1 \
      -p 5432 \
      -U "${POSTGRES_USER:-postgres}" \
      -d "${POSTGRES_DB:-pg_experiment_workbench}" \
      -At \
      -v ON_ERROR_STOP=1 \
      -c "SELECT 1" 2>/dev/null | grep -q '^1$'; then
      return 0
    fi
    sleep 1
  done

  echo "PgBouncer is not ready" >&2
  exit 1
}

wait_upgrade_server() {
  local service="$1"
  local label="$2"

  for _ in {1..90}; do
    if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T "$service" pg_isready \
      -h 127.0.0.1 \
      -p 5432 \
      -U "${POSTGRES_USER:-postgres}" \
      -d "${POSTGRES_DB:-pg_experiment_workbench}" >/dev/null 2>&1; then
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" exec -T "$service" env PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" psql \
        -h 127.0.0.1 \
        -p 5432 \
        -U "${POSTGRES_USER:-postgres}" \
        -d "${POSTGRES_DB:-pg_experiment_workbench}" \
        -At \
        -v ON_ERROR_STOP=1 \
        -c "SELECT 1" 2>/dev/null | grep -q '^1$'; then
        return 0
      fi
    fi
    sleep 1
  done

  echo "Upgrade $label server is not ready" >&2
  exit 1
}

up_upgrade() {
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile upgrade up -d postgres-old postgres-new
  wait_upgrade_server postgres-old old
  wait_upgrade_server postgres-new new
}

up_topology() {
  local topology="$1"
  require_topology "$topology"

  case "$topology" in
    source-tree)
      up_primary
      ;;
    single)
      up_primary
      ;;
    primary-replica)
      up_primary
      configure_primary_for_replica
      up_replica
      ;;
    logical-replication)
      up_primary
      up_logical_subscriber
      ;;
    pgbouncer)
      up_primary
      up_pgbouncer
      ;;
    multi-version-upgrade)
      up_upgrade
      ;;
  esac
}

reset_topology() {
  local topology="$1"
  require_topology "$topology"

  case "$topology" in
    source-tree)
      compose_down "" -v
      up_primary
      ;;
    single)
      compose_down "" -v
      up_primary
      ;;
    primary-replica)
      compose_down replica -v
      up_topology primary-replica
      ;;
    logical-replication)
      compose_down logical -v
      up_topology logical-replication
      ;;
    pgbouncer)
      compose_down pgbouncer -v
      up_topology pgbouncer
      ;;
    multi-version-upgrade)
      compose_down upgrade -v
      up_topology multi-version-upgrade
      ;;
  esac
}

down_topology() {
  local topology="$1"
  require_topology "$topology"

  case "$topology" in
    source-tree)
      compose_down ""
      ;;
    single)
      compose_down ""
      ;;
    primary-replica)
      compose_down replica
      ;;
    logical-replication)
      compose_down logical
      ;;
    pgbouncer)
      compose_down pgbouncer
      ;;
    multi-version-upgrade)
      compose_down upgrade
      ;;
  esac
}

status_topology() {
  local topology="$1"
  require_topology "$topology"

  case "$topology" in
    source-tree)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps postgres
      ;;
    single)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps postgres
      ;;
    primary-replica)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile replica ps postgres replica
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps --status running replica >/dev/null 2>&1; then
        printf '\nReplica recovery status:\n'
        replica_exec psql \
          -h 127.0.0.1 \
          -p 5432 \
          -U "${POSTGRES_USER:-postgres}" \
          -d "${POSTGRES_DB:-pg_experiment_workbench}" \
          -x \
          -v ON_ERROR_STOP=1 \
          -c "SELECT pg_is_in_recovery() AS in_recovery, pg_last_wal_receive_lsn() AS receive_lsn, pg_last_wal_replay_lsn() AS replay_lsn;"
      fi
      ;;
    logical-replication)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile logical ps postgres logical-subscriber
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps --status running logical-subscriber >/dev/null 2>&1; then
        printf '\nLogical subscription status:\n'
        logical_exec psql \
          -h 127.0.0.1 \
          -p 5432 \
          -U "${POSTGRES_USER:-postgres}" \
          -d "${POSTGRES_DB:-pg_experiment_workbench}" \
          -x \
          -v ON_ERROR_STOP=1 \
          -c "SELECT subname, pid, received_lsn, latest_end_lsn, latest_end_time FROM pg_stat_subscription ORDER BY subname;"
      fi
      ;;
    pgbouncer)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile pgbouncer ps postgres pgbouncer
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps --status running pgbouncer >/dev/null 2>&1; then
        printf '\nPgBouncer pools:\n'
        pgbouncer_exec env PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" psql \
          -h 127.0.0.1 \
          -p 5432 \
          -U "${POSTGRES_USER:-postgres}" \
          -d pgbouncer \
          -v ON_ERROR_STOP=1 \
          -c "SHOW POOLS;"
      fi
      ;;
    multi-version-upgrade)
      "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" --profile upgrade ps postgres-old postgres-new
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps --status running postgres-old >/dev/null 2>&1; then
        printf '\nOld PostgreSQL version:\n'
        upgrade_old_exec env PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" psql \
          -h 127.0.0.1 \
          -p 5432 \
          -U "${POSTGRES_USER:-postgres}" \
          -d "${POSTGRES_DB:-pg_experiment_workbench}" \
          -x \
          -v ON_ERROR_STOP=1 \
          -c "SELECT current_database() AS database, current_setting('server_version') AS server_version, current_setting('server_version_num') AS server_version_num;"
      fi
      if "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" ps --status running postgres-new >/dev/null 2>&1; then
        printf '\nNew PostgreSQL version:\n'
        upgrade_new_exec env PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" psql \
          -h 127.0.0.1 \
          -p 5432 \
          -U "${POSTGRES_USER:-postgres}" \
          -d "${POSTGRES_DB:-pg_experiment_workbench}" \
          -x \
          -v ON_ERROR_STOP=1 \
          -c "SELECT current_database() AS database, current_setting('server_version') AS server_version, current_setting('server_version_num') AS server_version_num;"
      fi
      ;;
  esac
}

wait_topology() {
  local topology="$1"
  require_topology "$topology"

  case "$topology" in
    source-tree)
      wait_primary
      ;;
    single)
      wait_primary
      ;;
    primary-replica)
      wait_primary
      wait_replica
      ;;
    logical-replication)
      wait_primary
      wait_logical_subscriber
      ;;
    pgbouncer)
      wait_primary
      wait_pgbouncer
      ;;
    multi-version-upgrade)
      wait_upgrade_server postgres-old old
      wait_upgrade_server postgres-new new
      ;;
  esac
}

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$ACTION" in
  help|-h|--help)
    usage
    ;;
  list)
    list_topologies
    ;;
  show)
    sed -n '1,220p' "$(resolve_topology_spec "${1:?topology is required}")"
    ;;
  up|reset|down|status|wait)
    load_repo_env
    compose_command
    "${ACTION}_topology" "${1:-${TOPOLOGY:-single}}"
    ;;
  *)
    load_repo_env
    compose_command
    up_topology "$ACTION"
    ;;
esac
